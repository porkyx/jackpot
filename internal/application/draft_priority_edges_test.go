// Permanent public draft boundary regressions; production owner remains unchanged.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/selection"
)

func receiptAuditCreate(t *testing.T, service *DraftService, id contracts.OperationID) contracts.DraftData {
	t.Helper()
	zero := contracts.Revision(0)
	data, err := service.Create(context.Background(), contracts.CreateDraftRequest{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: "session", OperationID: id, ExpectedRevision: &zero}})
	if err != nil {
		t.Fatal("existing public draft receipt rejected", id, err)
	}
	return data
}

func receiptAuditBound(t *testing.T, service *DraftService, expected int) {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.operations) != expected || len(service.order) != expected || expected > 128 {
		t.Fatalf("receipt cap/order mismatch: map=%d order=%d expected=%d", len(service.operations), len(service.order), expected)
	}
	seen := make(map[contracts.OperationID]bool, len(service.order))
	for _, id := range service.order {
		if seen[id] {
			t.Fatal("receipt order contains a duplicate identity", id)
		}
		seen[id] = true
		if _, found := service.operations[id]; !found {
			t.Fatal("receipt order refers to an evicted identity", id)
		}
	}
}

func receiptAuditState(t *testing.T, service *DraftService, id contracts.OperationID, expected contracts.OperationState) contracts.DraftOperationData {
	t.Helper()
	actual, err := service.Operation(context.Background(), id)
	if err != nil || actual.State != expected || actual.OperationID != id {
		t.Fatal("receipt state differs from literal oracle", id, actual, err)
	}
	if expected == contracts.OperationUnknown && (actual.Summary != nil || actual.FailureCode != nil) {
		t.Fatal("evicted receipt leaked a summary or failure", actual)
	}
	return actual
}

// A01: public Create(existing draft) adds receipts without creating another draft,
// changing counters, or starting collection. 127/128/129 are admitted receipt totals.
func TestDraftReceiptCapacity127128129EvictsOnlyTheOldestTerminalReceipt(t *testing.T) {
	var collectorCalls, extraIDs atomic.Int32
	service := newDraftHarness(t, testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
		collectorCalls.Add(1)
		return snapshotFixture(), nil
	}))
	service.options.NewID = func() string { extraIDs.Add(1); return "unexpected-extra-draft" }
	before := getDraft(t, service)
	for admitted := 2; admitted <= 129; admitted++ {
		id := contracts.OperationID(fmt.Sprintf("capacity-%03d", admitted))
		if data := receiptAuditCreate(t, service, id); !reflect.DeepEqual(data, before) {
			t.Fatal("receipt admission replaced existing draft", admitted)
		}
		if admitted == 127 || admitted == 128 || admitted == 129 {
			receiptAuditBound(t, service, min(admitted, 128))
			expectedOld := contracts.OperationSucceeded
			if admitted == 129 {
				expectedOld = contracts.OperationUnknown
			}
			receiptAuditState(t, service, "create", expectedOld)
			receiptAuditState(t, service, id, contracts.OperationSucceeded)
		}
	}
	// Same-ID replay cannot consume another slot or evict the next oldest receipt.
	service.mu.Lock()
	order := append([]contracts.OperationID{}, service.order...)
	service.mu.Unlock()
	receiptAuditCreate(t, service, "capacity-129")
	receiptAuditBound(t, service, 128)
	service.mu.Lock()
	orderUnchanged := reflect.DeepEqual(service.order, order)
	service.mu.Unlock()
	if !orderUnchanged || collectorCalls.Load() != 0 || extraIDs.Load() != 0 || !reflect.DeepEqual(before, getDraft(t, service)) {
		t.Fatal("replay changed receipt order, owner, IDs, or collection work")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

// A01: the loading and cancelling operands are separate cases. A real public
// CancelLoad is used; the collector intentionally waits at an owned channel so
// cancelling remains observable without replacing the production cancel function.
func TestDraftReceiptCapacityRetainsLiveLoadDuringLoadingAndCancellingThenEvictsTerminal(t *testing.T) {
	for _, phase := range []string{"loading", "cancelling"} {
		t.Run(phase, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			var collectorCalls atomic.Int32
			service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, _ func(CollectionProgress)) (CollectionSnapshot, error) {
				collectorCalls.Add(1)
				close(entered)
				<-release
				return snapshotFixture(), ctx.Err()
			}))
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				if err := service.Close(); err != nil {
					t.Error(err)
				}
			})
			load := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "bounded-live-load"), URL: "example"}
			if _, err := service.Load(context.Background(), load); err != nil {
				t.Fatal(err)
			}
			<-entered
			initialCount := 2 // create and Load
			if phase == "cancelling" {
				cancelled, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "bounded-cancel"), Kind: "CancelLoad"})
				if err != nil || cancelled.Load == nil || cancelled.Load.State != "cancelling" {
					t.Fatal("public cancel did not admit cancelling", cancelled.Load, err)
				}
				initialCount++
			}
			before := getDraft(t, service)
			for admitted := initialCount + 1; admitted <= 132; admitted++ {
				id := contracts.OperationID(fmt.Sprintf("live-receipt-%03d", admitted))
				if data := receiptAuditCreate(t, service, id); !reflect.DeepEqual(data, before) {
					t.Fatal("receipt admission changed live draft", admitted)
				}
				if admitted >= 127 {
					receiptAuditBound(t, service, min(admitted, 128))
					receiptAuditState(t, service, load.OperationID, contracts.OperationPending)
					if state := getDraft(t, service); state.Load == nil || state.Load.State != phase || !reflect.DeepEqual(state, before) {
						t.Fatal("capacity eviction retired or changed live work", admitted, state.Load)
					}
				}
			}
			if collectorCalls.Load() != 1 {
				t.Fatal("receipt pressure started duplicate collection")
			}
			receiptAuditState(t, service, "create", contracts.OperationUnknown)
			releaseOnce.Do(func() { close(release) })
			service.workers.Wait()
			terminal := getDraft(t, service)
			expectedTerminal := "completed"
			if phase == "cancelling" {
				expectedTerminal = "cancelled"
			}
			if terminal.Load == nil || terminal.Load.State != expectedTerminal {
				t.Fatal("worker did not reach the admitted terminal", terminal.Load)
			}
			// A cancelled ephemeral Load is a successfully applied cancellation,
			// not a fabricated durable OperationFailed.
			receiptAuditState(t, service, load.OperationID, contracts.OperationSucceeded)
			for index := 0; index < 128; index++ {
				receiptAuditCreate(t, service, contracts.OperationID(fmt.Sprintf("terminal-pressure-%03d", index)))
			}
			receiptAuditBound(t, service, 128)
			receiptAuditState(t, service, load.OperationID, contracts.OperationUnknown)
			service.mu.Lock()
			leaseReleased := service.cancel == nil
			service.mu.Unlock()
			if !leaseReleased || collectorCalls.Load() != 1 || !reflect.DeepEqual(terminal, getDraft(t, service)) {
				t.Fatal("terminal eviction changed owner or retained worker lease")
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A02: after the public reset !closed is true but Load is nil. No closed-state
// or terminal-State operand is used to mask the nil operand.
func TestDraftOldProgressAfterPublicResetCannotRecreateNullLoad(t *testing.T) {
	callbacks := make(chan func(CollectionProgress), 1)
	var calls atomic.Int32
	service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		calls.Add(1)
		callbacks <- progress
		return snapshotFixture(), ctx.Err()
	}))
	request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "reset-progress-load"), URL: "example"}
	if _, err := service.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	callback := <-callbacks
	service.workers.Wait()
	oldReceipt := receiptAuditState(t, service, request.OperationID, contracts.OperationSucceeded)
	reset, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "reset-progress-article"), Kind: "ResetArticle"})
	if err != nil || reset.Load != nil || reset.Article != nil || reset.Summary.Snapshot != nil || reset.Summary.State != contracts.DraftEmpty {
		t.Fatal("public reset did not establish null-Load predecessor", reset, err)
	}
	callback(CollectionProgress{Pages: 20, Comments: 100000})
	if !reflect.DeepEqual(reset, getDraft(t, service)) || !reflect.DeepEqual(oldReceipt, receiptAuditState(t, service, request.OperationID, contracts.OperationSucceeded)) || calls.Load() != 1 {
		t.Fatal("retired callback recreated progress or changed old receipt")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

// A02: while L2 is actively loading, the closed/present/state operands are all
// eligible. Only the retained L1 callback's operation identity differs.
func TestDraftOldProgressCannotModifyAnotherActivelyLoadingOperation(t *testing.T) {
	callbacks := make(chan func(CollectionProgress), 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		ordinal := calls.Add(1)
		callbacks <- progress
		if ordinal == 2 {
			<-release
		}
		return snapshotFixture(), ctx.Err()
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	oldLoad := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "old-progress-owner"), URL: "example"}
	if _, err := service.Load(context.Background(), oldLoad); err != nil {
		t.Fatal(err)
	}
	oldCallback := <-callbacks
	service.workers.Wait()
	oldReceipt := receiptAuditState(t, service, oldLoad.OperationID, contracts.OperationSucceeded)
	newLoad := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "new-progress-owner"), URL: "example"}
	if _, err := service.Load(context.Background(), newLoad); err != nil {
		t.Fatal(err)
	}
	newCallback := <-callbacks
	before := getDraft(t, service)
	if before.Load == nil || before.Load.State != "loading" || before.Load.OperationID != newLoad.OperationID || before.Load.Sequence != 0 {
		t.Fatal("new operation is not eligible for its own callback", before.Load)
	}
	var staleWorkers sync.WaitGroup
	for index := 0; index < 32; index++ {
		staleWorkers.Add(1)
		go func() { defer staleWorkers.Done(); oldCallback(CollectionProgress{Pages: 20, Comments: 100000}) }()
	}
	staleWorkers.Wait()
	if !reflect.DeepEqual(before, getDraft(t, service)) || !reflect.DeepEqual(oldReceipt, receiptAuditState(t, service, oldLoad.OperationID, contracts.OperationSucceeded)) {
		t.Fatal("foreign callback changed current draft or retired receipt")
	}
	newCallback(CollectionProgress{Pages: 1, Comments: 2})
	current := getDraft(t, service)
	if current.Load.Sequence != 1 || current.Load.Pages != 1 || current.Load.Comments != 2 || current.Load.OperationID != newLoad.OperationID || current.Summary.Revision != before.Summary.Revision || current.Summary.ArticleGeneration != before.Summary.ArticleGeneration {
		t.Fatal("current callback failed to advance only current progress", current.Load)
	}
	releaseOnce.Do(func() { close(release) })
	service.workers.Wait()
	complete := getDraft(t, service)
	if complete.Load.State != "completed" || complete.Load.Sequence != 2 || complete.Summary.ArticleGeneration != before.Summary.ArticleGeneration+1 || complete.Summary.Revision != before.Summary.Revision+1 || calls.Load() != 2 {
		t.Fatal("new operation did not complete exactly once", complete.Load)
	}
	service.mu.Lock()
	leaseReleased := service.cancel == nil
	service.mu.Unlock()
	if !leaseReleased {
		t.Fatal("completed new operation retained worker lease")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func promotionAuditOwnerJSON(t *testing.T, service *DraftService) string {
	t.Helper()
	service.mu.Lock()
	raw, err := json.Marshal(struct {
		Participants []selection.Participant
		Author       *selection.ParticipantKey
	}{service.participants, service.author})
	service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A07: unknown PostedAt is legal collected source data, but cannot decide a
// configured timecut. It must reach counts(), then roll back the new owner.
func TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease(t *testing.T) {
	service, _ := configuredDraftSnapshot(t)
	cut := draftNow.Add(time.Minute)
	filters := getDraft(t, service).Filters
	filters.TimeCut = &cut
	if _, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "timecut-configuration"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	old := getDraft(t, service)
	oldOwner := promotionAuditOwnerJSON(t, service)
	var unknown atomic.Bool
	unknown.Store(true)
	var calls, notices atomic.Int32
	callbacks := make(chan func(CollectionProgress), 4)
	service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
	service.options.Collector = testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		calls.Add(1)
		callbacks <- progress
		progress(CollectionProgress{Pages: 1, Comments: 2})
		next := snapshotFixture()
		next.Article.Title = "replacement must not promote while time is unknown"
		next.Author = &selection.ParticipantKey{Nickname: "다른 작성자", Identifier: "other-author", Anonymous: false}
		if unknown.Load() {
			next.Comments[0].PostedAt = nil
		}
		return next, ctx.Err()
	})
	// Prove the failure fixture reaches promotion validation, rather than the
	// existing invalid-key/BuildParticipants early failure path.
	legalUnknown := snapshotFixture()
	legalUnknown.Comments[0].PostedAt = nil
	if built, err := selection.BuildParticipants(legalUnknown.Comments); err != nil || len(built) != 2 {
		t.Fatal("unknown timestamp is not legal collected source data", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, fmt.Sprintf("unknown-time-load-%d", attempt)), URL: "example"}
		loading, err := service.Load(context.Background(), request)
		if err != nil || loading.Load.State != "loading" {
			t.Fatal("load admission failed", err)
		}
		callback := <-callbacks
		service.workers.Wait()
		failed := getDraft(t, service)
		if failed.Load.State != "failed" || failed.Load.FailureCode == nil || *failed.Load.FailureCode != contracts.InvalidInput || failed.Load.Sequence != 2 || failed.Summary.Revision != old.Summary.Revision+contracts.Revision((attempt+1)*2) || failed.Summary.ArticleGeneration != old.Summary.ArticleGeneration {
			t.Fatal("unknown time promoted, wrapped counters, or remained pending", failed.Load)
		}
		assertPreviousCollectionPreserved(t, service, old)
		if promotionAuditOwnerJSON(t, service) != oldOwner {
			t.Fatal("promotion failure replaced participant/manual/author owner")
		}
		service.mu.Lock()
		leaseReleased := service.cancel == nil
		service.mu.Unlock()
		if !leaseReleased {
			t.Fatal("failed promotion retained worker lease")
		}
		receipt := receiptAuditState(t, service, request.OperationID, contracts.OperationFailed)
		if receipt.FailureCode == nil || *receipt.FailureCode != contracts.InvalidInput {
			t.Fatal("failure receipt lost safe reason", receipt)
		}
		replay, err := service.Load(context.Background(), request)
		if err != nil || !reflect.DeepEqual(replay, failed) || calls.Load() != int32(attempt+1) || notices.Load() != int32((attempt+1)*2) {
			t.Fatal("same-ID failed replay restarted collection or changed owner", err)
		}
		callback(CollectionProgress{Pages: 20, Comments: 100000})
		if !reflect.DeepEqual(failed, getDraft(t, service)) {
			t.Fatal("late failure callback changed preserved owner")
		}
		// Returned projection and receipt must remain detached after rollback.
		baseline := getDraft(t, service)
		failed.Article.Title = "caller mutation"
		failed.Summary.Snapshot.Complete = false
		failed.Prizes.Multiple[0].Name = "caller mutation"
		*failed.Load.FailureCode = contracts.InvalidState
		receipt.Summary.Snapshot.Pages = 20
		*receipt.FailureCode = contracts.InvalidState
		receiptAgain := receiptAuditState(t, service, request.OperationID, contracts.OperationFailed)
		if !reflect.DeepEqual(baseline, getDraft(t, service)) || receiptAgain.FailureCode == nil || *receiptAgain.FailureCode != contracts.InvalidInput || promotionAuditOwnerJSON(t, service) != oldOwner {
			t.Fatal("failed response aliases preserved draft/receipt/participant owner")
		}
	}
	// An explicit new operation with known time is legal; failed same-ID is not
	// automatically resent, and the failed operations retain their old receipts.
	unknown.Store(false)
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "known-time-corrected-load"), URL: "example"}); err != nil {
		t.Fatal("failed load lease not reusable", err)
	}
	<-callbacks
	service.workers.Wait()
	complete := getDraft(t, service)
	if complete.Load.State != "completed" || complete.Summary.ArticleGeneration != old.Summary.ArticleGeneration+1 || complete.Summary.Revision != old.Summary.Revision+8 || calls.Load() != 4 || notices.Load() != 8 || complete.Participants != 2 || complete.Included != 1 || complete.Excluded != 1 || !complete.AuthorIdentifiable || complete.Article.Title != "replacement must not promote while time is unknown" || !reflect.DeepEqual(complete.Filters, old.Filters) || !reflect.DeepEqual(complete.Prizes, old.Prizes) {
		t.Fatal("corrected source failed to complete exactly once", complete.Load)
	}
	for attempt := 0; attempt < 3; attempt++ {
		receiptAuditState(t, service, contracts.OperationID(fmt.Sprintf("unknown-time-load-%d", attempt)), contracts.OperationFailed)
	}
	service.mu.Lock()
	leaseReleased := service.cancel == nil
	service.mu.Unlock()
	if !leaseReleased {
		t.Fatal("corrected completed load retained lease")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}
