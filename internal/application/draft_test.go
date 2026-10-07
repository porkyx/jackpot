package application

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/selection"
)

type testCollector func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error)

func (collector testCollector) Collect(ctx context.Context, url string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
	return collector(ctx, url, progress)
}

var draftNow = time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC)

func snapshotFixture() CollectionSnapshot {
	return CollectionSnapshot{Article: contracts.ArticleData{URL: "https://gall.dcinside.com/board/view/?id=example&no=1", Title: "<script>한글</script>", GalleryID: "example", Number: "1"}, Comments: []selection.InputComment{
		{ID: "c1", Nickname: "가", Identifier: "user1", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "hello", PostedAt: &draftNow},
		{ID: "c2", Nickname: "나", Identifier: "public-ip", ParticipantKind: selection.Anonymous, Kind: selection.Text, Text: "world", PostedAt: &draftNow}}, CollectedAt: draftNow, Pages: 1}
}
func newDraftHarness(t *testing.T, collector Collector) *DraftService {
	t.Helper()
	var ids atomic.Uint32
	service, err := NewDraftService(DraftOptions{Session: "session", Now: func() time.Time { return draftNow }, NewID: func() string {
		if ids.Add(1) == 1 {
			return "draft"
		}
		return "draft2"
	}, Collector: collector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
	})
	zero := contracts.Revision(0)
	_, err = service.Create(context.Background(), contracts.CreateDraftRequest{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: "session", OperationID: "create", ExpectedRevision: &zero}})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func draftHeader(service *DraftService, op string) contracts.DraftMutationHeader {
	summary := service.Summary()
	revision, generation := summary.Revision, summary.ArticleGeneration
	return contracts.DraftMutationHeader{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: summary.BackendSessionID, OperationID: contracts.OperationID(op), ExpectedRevision: &revision}, DraftID: summary.DraftID, ArticleGeneration: &generation}
}
func completedDraft(t *testing.T) *DraftService {
	t.Helper()
	service := newDraftHarness(t, testCollector(func(ctx context.Context, url string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		progress(CollectionProgress{Pages: 1, Comments: 2})
		return snapshotFixture(), nil
	}))
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	service.workers.Wait()
	return service
}
func getDraft(t *testing.T, service *DraftService) contracts.DraftData {
	t.Helper()
	data, err := service.Get(context.Background(), contracts.DraftQuery{BackendSessionID: "session", DraftID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestCollectionPromotesOnlyCompleteSnapshotAndPagesAreDetached(t *testing.T) {
	service := completedDraft(t)
	data := getDraft(t, service)
	if data.Summary.State != contracts.DraftReady || data.Summary.ArticleGeneration != 1 || data.Summary.Revision != 2 || data.Participants != 2 || data.Included != 2 || data.Excluded != 0 || data.Load.State != "completed" {
		t.Fatalf("invalid completed projection: %+v", data)
	}
	page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: data.Summary.DraftContext, Limit: 100})
	if err != nil || len(page.Rows) != 2 || page.Total != 2 || page.Matched != 2 {
		t.Fatalf("page: %+v %v", page, err)
	}
	page.Rows[0].Nickname = "changed"
	data.Article.Title = "changed"
	data.Filters.IncludeKeywords = append(data.Filters.IncludeKeywords, "evil")
	again := getDraft(t, service)
	if again.Article.Title != "<script>한글</script>" || len(again.Filters.IncludeKeywords) != 0 {
		t.Fatal("projection mutated owner")
	}
	comments, err := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: again.Summary.DraftContext, ParticipantID: page.Rows[0].ID, Limit: 50})
	if err != nil || len(comments.Rows) != 1 {
		t.Fatalf("comments %v %+v", err, comments)
	}
	*comments.Rows[0].PostedAt = draftNow.Add(time.Hour)
	second, _ := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: again.Summary.DraftContext, ParticipantID: page.Rows[0].ID, Limit: 50})
	if !second.Rows[0].PostedAt.Equal(draftNow) {
		t.Fatal("comment time aliases owner")
	}
}
func TestCollectionFailurePreservesPreviousSnapshotFiltersAndManualValues(t *testing.T) {
	for _, failure := range []error{errors.New("first call transport"), contracts.NewFault(contracts.InvalidInput), context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			service := completedDraft(t)
			old := getDraft(t, service)
			service.options.Collector = testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
				return CollectionSnapshot{}, failure
			})
			request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "failed-load"), URL: "example"}
			if _, err := service.Load(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			service.workers.Wait()
			actual := getDraft(t, service)
			if !reflect.DeepEqual(actual.Summary.Snapshot, old.Summary.Snapshot) || actual.Summary.ArticleGeneration != old.Summary.ArticleGeneration || actual.Summary.State != contracts.DraftReady || actual.Load.State != "failed" || actual.Participants != 2 {
				t.Fatalf("failure changed snapshot %+v", actual)
			}
			if _, err := service.Load(context.Background(), request); err != nil {
				t.Fatalf("replay must precede stale revision: %v", err)
			}
			request.URL = "different"
			if _, err := service.Load(context.Background(), request); err == nil {
				t.Fatal("same ID different payload accepted")
			}
		})
	}
}
func TestInitialFailureRestoresEmptyRatherThanEmptySuccess(t *testing.T) {
	service := newDraftHarness(t, testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
		return CollectionSnapshot{}, errors.New("failure")
	}))
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	service.workers.Wait()
	actual := getDraft(t, service)
	if actual.Summary.State != contracts.DraftEmpty || actual.Summary.Snapshot != nil || actual.Load.State != "failed" || actual.Summary.ArticleGeneration != 0 {
		t.Fatalf("failure disguised: %+v", actual)
	}
}
func TestCancelWaitsForCollectorTerminalAndDoesNotErasePriorSnapshot(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	service := completedDraft(t)
	old := getDraft(t, service)
	service.options.Collector = testCollector(func(ctx context.Context, url string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		close(started)
		<-ctx.Done()
		<-released
		return CollectionSnapshot{}, ctx.Err()
	})
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "second-load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	<-started
	cancellation := contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "cancel"), Kind: "CancelLoad"}
	data, err := service.Edit(context.Background(), cancellation)
	if err != nil || data.Load.State != "cancelling" {
		t.Fatalf("cancel prematurely terminal: %+v %v", data, err)
	}
	close(released)
	service.workers.Wait()
	actual := getDraft(t, service)
	if actual.Load.State != "cancelled" || actual.Summary.State != contracts.DraftReady || !reflect.DeepEqual(actual.Summary.Snapshot, old.Summary.Snapshot) {
		t.Fatal("cancel replaced prior snapshot")
	}
}
func TestManualOverridesAndSeparateResetsPreserveEachOther(t *testing.T) {
	service := completedDraft(t)
	data := getDraft(t, service)
	page, _ := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: data.Summary.DraftContext, Limit: 100})
	filters := data.Filters
	filters.ExcludeAnonymous = true
	filtered, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "filters"), Kind: "UpdateFilters", Filters: &filters})
	if err != nil || filtered.Included != 1 {
		t.Fatalf("filter %+v %v", filtered, err)
	}
	included, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "toggle"), Kind: "ToggleParticipant", ParticipantID: page.Rows[1].ID})
	if err != nil || included.Included != 2 {
		t.Fatal("auto-excluded manual include failed")
	}
	reset, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "reset-filter"), Kind: "ResetFilters"})
	if err != nil || reset.Filters.ExcludeAuthor || reset.Included != 2 {
		t.Fatal("filter reset restored default instead of all-off")
	}
	_, err = service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "filter-again"), Kind: "UpdateFilters", Filters: &filters})
	if err != nil {
		t.Fatal(err)
	}
	actual := getDraft(t, service)
	if actual.Included != 2 {
		t.Fatal("filter change lost manual override")
	}
	actual, err = service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "reset-manual"), Kind: "ResetManual"})
	if err != nil || actual.Included != 1 || !actual.Filters.ExcludeAnonymous {
		t.Fatal("manual reset changed filters")
	}
}
func TestInvalidEditsRollbackStateRevisionAndDoNotConsumeOperationID(t *testing.T) {
	for _, kind := range []string{"Unknown", "UpdateFilters", "SetPrizes", "SetAllUnclassified", "ToggleParticipant"} {
		t.Run(kind, func(t *testing.T) {
			service := completedDraft(t)
			old := getDraft(t, service)
			request := contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "invalid"), Kind: kind}
			if _, err := service.Edit(context.Background(), request); err == nil {
				t.Fatal("invalid command accepted")
			}
			if !reflect.DeepEqual(getDraft(t, service), old) {
				t.Fatal("failed command mutated draft")
			}
			if _, ok := service.operations["invalid"]; ok {
				t.Fatal("invalid command consumed ID")
			}
		})
	}
}
func TestResetArticleClearsArticleAndFiltersPreservesPrizeModesAndIncrementsGeneration(t *testing.T) {
	service := completedDraft(t)
	old := getDraft(t, service)
	prizes := old.Prizes
	prizes.DrawMode = "reservation"
	prizes.Mode = "multiple"
	prizes.Multiple = []contracts.PrizeInput{{ID: "x", Name: "이름", Count: 2}}
	if _, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "prizes"), Kind: "SetPrizes", Prizes: &prizes}); err != nil {
		t.Fatal(err)
	}
	actual, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "reset"), Kind: "ResetArticle"})
	if err != nil || actual.Article != nil || actual.Summary.Snapshot != nil || actual.Summary.ArticleGeneration != 2 || actual.Participants != 0 || actual.Filters.ExcludeAuthor || !reflect.DeepEqual(actual.Prizes, prizes) {
		t.Fatalf("reset %+v %v", actual, err)
	}
}
func TestQueriesRejectInvalidLimitTargetAndStaleContext(t *testing.T) {
	service := completedDraft(t)
	data := getDraft(t, service)
	for _, limit := range []uint32{0, 101, ^uint32(0)} {
		if _, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: data.Summary.DraftContext, Limit: limit}); err == nil {
			t.Fatalf("participant limit %d accepted", limit)
		}
	}
	for _, limit := range []uint32{0, 51, ^uint32(0)} {
		if _, err := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: data.Summary.DraftContext, ParticipantID: "missing", Limit: limit}); err == nil {
			t.Fatalf("comment limit %d accepted", limit)
		}
	}
	stale := data.Summary.DraftContext
	stale.Revision++
	if _, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: stale, Limit: 1}); err == nil {
		t.Fatal("stale revision accepted")
	}
	page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: data.Summary.DraftContext, Limit: 1, Offset: ^uint32(0)})
	if err != nil || len(page.Rows) != 0 || page.Total != 2 || page.Matched != 2 {
		t.Fatalf("large offset changed totals: %+v %v", page, err)
	}
}
func TestCloseCancelsWorkerAndRepeatedCloseIsIdempotent(t *testing.T) {
	started := make(chan struct{})
	service := newDraftHarness(t, testCollector(func(ctx context.Context, url string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		close(started)
		<-ctx.Done()
		return CollectionSnapshot{}, ctx.Err()
	}))
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background(), contracts.DraftQuery{BackendSessionID: "session", DraftID: "draft"}); err == nil {
		t.Fatal("closed owner returned live projection")
	}
}
func TestNilAndCancelledRequestsHaveNoSideEffects(t *testing.T) {
	service := completedDraft(t)
	before := getDraft(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, requestCtx := range []context.Context{nil, ctx} {
		if _, err := service.Edit(requestCtx, contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "no-side-effect"), Kind: "ResetArticle"}); err == nil {
			t.Fatal("invalid context accepted")
		}
		if !reflect.DeepEqual(before, getDraft(t, service)) {
			t.Fatal("invalid context changed state")
		}
	}
}

func TestClosedDraftQueriesRejectPreviouslyValidContexts(t *testing.T) {
	service := completedDraft(t)
	draftContext := service.Summary().DraftContext
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: draftContext, Limit: 100}); err == nil {
		t.Fatal("closed participant read accepted")
	}
	if _, err := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: draftContext, Limit: 50, ParticipantID: "missing"}); err == nil {
		t.Fatal("closed comment read accepted")
	}
}
func TestTypedNilCollectorIsRejectedBeforeAnyWork(t *testing.T) {
	var collector testCollector
	if service, err := NewDraftService(DraftOptions{Session: "session", Now: func() time.Time { return draftNow }, NewID: func() string { return "draft" }, Collector: collector}); service != nil || err == nil {
		t.Fatalf("%v/%v", service, err)
	}
}
func TestFreezePreservesSinglePrizeIdentityAndUnicodeName(t *testing.T) {
	service := completedDraft(t)
	service.mu.Lock()
	defer service.mu.Unlock()
	service.draft.Prizes.Single.ID = "single-choice"
	service.draft.Prizes.Single.Name = "E2E 상품😀"
	request := contracts.CreateCollectionRequest{DraftMutationHeader: contracts.DraftMutationHeader{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: "session", OperationID: "freeze-name", ExpectedRevision: &service.draft.Summary.Revision}, DraftID: service.draft.Summary.DraftID, ArticleGeneration: &service.draft.Summary.ArticleGeneration}, Mode: "immediate"}
	_, input, err := service.freeze(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Prizes) != 1 || input.Prizes[0].ID != "single-choice" || input.Prizes[0].Name != "E2E 상품😀" {
		t.Fatalf("name lost %+v", input.Prizes)
	}
}

func TestParticipantGroupTotalStaysDistinctFromSearchMatchAndNeverMutatesSelection(t *testing.T) {
	service := completedDraft(t)
	filters := getDraft(t, service).Filters
	filters.ExcludeAnonymous = true
	if _, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "group-filter"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	before := getDraft(t, service)
	for _, entry := range []struct {
		group, query   string
		total, matched uint32
	}{{"excluded", "", 1, 1}, {"excluded", "no match", 1, 0}, {"unclassified", "USER1", 1, 1}, {"included", "", 0, 0}, {"", "", 2, 2}, {"", "USER1", 2, 1}} {
		page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: before.Summary.DraftContext, Group: entry.group, Query: entry.query, Limit: 100})
		if err != nil || page.Total != entry.total || page.Matched != entry.matched || uint32(len(page.Rows)) != entry.matched {
			t.Fatalf("group=%q query=%q page=%+v err=%v", entry.group, entry.query, page, err)
		}
	}
	if !reflect.DeepEqual(before, getDraft(t, service)) {
		t.Fatal("read mutated draft/filter/manual/revision")
	}
}
