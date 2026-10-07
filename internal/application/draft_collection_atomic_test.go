package application

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func configuredDraftSnapshot(t *testing.T) (*DraftService, contracts.DraftData) {
	t.Helper()
	service := completedDraft(t)
	data := getDraft(t, service)
	filters := data.Filters
	filters.ExcludeAnonymous = true
	filters.IncludeKeywords = []string{"hello"}
	if _, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "configure-filter"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: getDraft(t, service).Summary.DraftContext, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "manual-include"), Kind: "ToggleParticipant", ParticipantID: page.Rows[1].ID}); err != nil {
		t.Fatal(err)
	}
	prizes := getDraft(t, service).Prizes
	prizes.Single.Name = "상품 한글😀"
	prizes.Multiple[0].Name = "다른 품목"
	prizes.DrawMode = "reservation"
	if _, err = service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "configure-prizes"), Kind: "SetPrizes", Prizes: &prizes}); err != nil {
		t.Fatal(err)
	}
	return service, getDraft(t, service)
}

func assertPreviousCollectionPreserved(t *testing.T, service *DraftService, old contracts.DraftData) {
	t.Helper()
	current := getDraft(t, service)
	if current.Summary.State != contracts.DraftReady || current.Summary.ArticleGeneration != old.Summary.ArticleGeneration || !reflect.DeepEqual(current.Summary.Snapshot, old.Summary.Snapshot) || !reflect.DeepEqual(current.Article, old.Article) || !reflect.DeepEqual(current.Filters, old.Filters) || !reflect.DeepEqual(current.Prizes, old.Prizes) || current.Participants != old.Participants || current.Included != old.Included || current.Excluded != old.Excluded {
		t.Fatalf("failed collection changed prior state: old=%+v current=%+v", old, current)
	}
	page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: current.Summary.DraftContext, Limit: 100})
	if err != nil || len(page.Rows) != 2 || !page.Rows[1].Included {
		t.Fatalf("prior manual include was lost: %+v %v", page, err)
	}
}

func TestAcceptedCancelWinsBeforeContextSignalEvenWhenCollectorReturnsSuccess(t *testing.T) {
	service, old := configuredDraftSnapshot(t)
	started, releaseSuccess, cancelInvoked, releaseCancel := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var successOnce, cancelOnce sync.Once
	defer successOnce.Do(func() { close(releaseSuccess) })
	defer cancelOnce.Do(func() { close(releaseCancel) })
	service.options.Collector = testCollector(func(ctx context.Context, _ string, _ func(CollectionProgress)) (CollectionSnapshot, error) {
		close(started)
		select {
		case <-releaseSuccess:
			next := snapshotFixture()
			next.Article.Title = "new snapshot must not promote"
			return next, nil
		case <-ctx.Done():
			return CollectionSnapshot{}, ctx.Err()
		}
	})
	load := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "cancel-race-load"), URL: "example"}
	if _, err := service.Load(context.Background(), load); err != nil {
		t.Fatal(err)
	}
	<-started
	service.mu.Lock()
	actualCancel := service.cancel
	service.cancel = func() { close(cancelInvoked); <-releaseCancel; actualCancel() }
	service.mu.Unlock()
	cancelRequest := contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "accepted-cancel"), Kind: "CancelLoad"}
	cancelled := make(chan error, 1)
	go func() { _, err := service.Edit(context.Background(), cancelRequest); cancelled <- err }()
	<-cancelInvoked
	if state := getDraft(t, service).Load.State; state != "cancelling" {
		t.Fatalf("cancel was not admitted: %s", state)
	}
	successOnce.Do(func() { close(releaseSuccess) })
	service.workers.Wait()
	cancelOnce.Do(func() { close(releaseCancel) })
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	current := getDraft(t, service)
	if current.Load.State != "cancelled" || current.Load.FailureCode != nil {
		t.Fatalf("admitted cancellation became successful promotion: %+v", current.Load)
	}
	assertPreviousCollectionPreserved(t, service, old)
	replay, err := service.Load(context.Background(), load)
	if err != nil || replay.Load.State != "cancelled" || replay.Summary.Revision != current.Summary.Revision {
		t.Fatalf("load replay invented another terminal state: %+v %v", replay, err)
	}
}

func TestCancellationAfterPromotionKeepsCompletedGenerationAndIsIdempotent(t *testing.T) {
	service := completedDraft(t)
	old := getDraft(t, service)
	request := contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "cancel-completed"), Kind: "CancelLoad"}
	for attempt := 0; attempt < 2; attempt++ {
		current, err := service.Edit(context.Background(), request)
		if err != nil || !reflect.DeepEqual(current, old) {
			t.Fatalf("post-promotion cancellation changed completed snapshot: %+v %v", current, err)
		}
	}
}

func TestRejectedCollectionMetadataAndFailuresKeepConfiguredSnapshotAndManualInclude(t *testing.T) {
	cases := []struct {
		name    string
		alter   func(*CollectionSnapshot)
		failure error
	}{
		{name: "page-twenty-one", alter: func(s *CollectionSnapshot) { s.Pages = 21 }},
		{name: "missing-collected-time", alter: func(s *CollectionSnapshot) { s.CollectedAt = time.Time{} }},
		{name: "non-utc-collected-time", alter: func(s *CollectionSnapshot) { s.CollectedAt = draftNow.In(time.FixedZone("KST", 9*60*60)) }},
		{name: "out-of-range-year", alter: func(s *CollectionSnapshot) { s.CollectedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{name: "invalid-participant-key", alter: func(s *CollectionSnapshot) { s.Comments[1].Identifier = "" }},
		{name: "transport", failure: errors.New("unavailable")},
		{name: "deadline", failure: context.DeadlineExceeded},
	}
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			service, old := configuredDraftSnapshot(t)
			service.options.Collector = testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
				next := snapshotFixture()
				if entry.alter != nil {
					entry.alter(&next)
				}
				return next, entry.failure
			})
			for attempt := 0; attempt < 3; attempt++ {
				operation := contracts.OperationID(entry.name + string(rune('a'+attempt)))
				if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, string(operation)), URL: "example"}); err != nil {
					t.Fatal(err)
				}
				service.workers.Wait()
				current := getDraft(t, service)
				if current.Load.State != "failed" || current.Load.FailureCode == nil {
					t.Fatalf("invalid or failed collection reported completion: %+v", current.Load)
				}
				assertPreviousCollectionPreserved(t, service, old)
			}
		})
	}
}

func TestSuccessfulRecollectionClearsManualIncludeAndKeepsFilterAndBothPrizeModes(t *testing.T) {
	service, old := configuredDraftSnapshot(t)
	if old.Included != 2 {
		t.Fatal("manual setup did not include both participants")
	}
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "fresh-complete"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	service.workers.Wait()
	current := getDraft(t, service)
	if current.Load.State != "completed" || current.Summary.ArticleGeneration != old.Summary.ArticleGeneration+1 || !reflect.DeepEqual(current.Filters, old.Filters) || !reflect.DeepEqual(current.Prizes, old.Prizes) || current.Included != 1 || current.Excluded != 1 {
		t.Fatalf("recollection reset wrong owners: %+v", current)
	}
	page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: current.Summary.DraftContext, Limit: 100})
	if err != nil || len(page.Rows) != 2 || page.Rows[1].Included {
		t.Fatalf("manual override survived recollection: %+v %v", page, err)
	}
}
