package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/porkyx/jackpot/internal/application"
	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/selection"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

type roundCollector struct{ now time.Time }

func (collector roundCollector) Collect(ctx context.Context, url string, progress func(app.CollectionProgress)) (app.CollectionSnapshot, error) {
	progress(app.CollectionProgress{Pages: 1, Comments: 3})
	return app.CollectionSnapshot{Article: contracts.ArticleData{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "한글 Alpha 결과", GalleryName: "ÖGG 갤러리", GalleryID: "test", Number: "1"}, Comments: []selection.InputComment{{ID: "one", Nickname: "가", Identifier: "user1", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "one", PostedAt: &collector.now}, {ID: "two", Nickname: "나", Identifier: "public-ip", ParticipantKind: selection.Anonymous, Kind: selection.Text, Text: "two", PostedAt: &collector.now}, {ID: "three", Nickname: "다", Identifier: "user3", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "three", PostedAt: &collector.now}}, CollectedAt: collector.now, Pages: 1}, ctx.Err()
}

type roundBoundaryEntropy struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (entropy *roundBoundaryEntropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	entropy.calls.Add(1)
	if entropy.fail.Load() {
		return 0, contracts.NewFault(contracts.InvalidState)
	}
	return 0, ctx.Err()
}
func roundDraftHeader(draft *app.DraftService, operation string) contracts.DraftMutationHeader {
	summary := draft.Summary()
	revision, generation := summary.Revision, summary.ArticleGeneration
	return contracts.DraftMutationHeader{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: summary.BackendSessionID, OperationID: contracts.OperationID(operation), ExpectedRevision: &revision}, DraftID: summary.DraftID, ArticleGeneration: &generation}
}
func roundStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "desktop-product.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
func roundDraft(t *testing.T, session contracts.BackendSessionID, now func() time.Time, loaded bool) *app.DraftService {
	t.Helper()
	var draft *app.DraftService
	ready := make(chan struct{}, 1)
	draft, err := app.NewDraftService(app.DraftOptions{Session: session, Now: now, NewID: func() string { return "desktop-draft" }, Collector: roundCollector{now: now()}, Publish: func(notice contracts.StateNotice) error {
		if draft.Summary() != nil && draft.Summary().State == contracts.DraftReady {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := draft.Close(); err != nil {
			t.Error(err)
		}
	})
	if !loaded {
		return draft
	}
	zero := contracts.Revision(0)
	if _, err = draft.Create(context.Background(), contracts.CreateDraftRequest{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: session, OperationID: "draft-create", ExpectedRevision: &zero}}); err != nil {
		t.Fatal(err)
	}
	if _, err = draft.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: roundDraftHeader(draft, "draft-load"), URL: "test"}); err != nil {
		t.Fatal(err)
	}
	<-ready
	return draft
}
func roundBoundary(t *testing.T, store *sqlite.Store, draft *app.DraftService, session contracts.BackendSessionID, now func() time.Time, entropy *roundBoundaryEntropy) (*RoundService, *rl.Service) {
	t.Helper()
	var ids atomic.Uint32
	lifecycle, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: session, Entropy: entropy, Clock: now, NewID: func() string { return fmt.Sprintf("desktop-id-%d", ids.Add(1)) }, AppVersion: "desktop-test-version"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lifecycle.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	boundary, err := NewRoundService(draft, lifecycle, store, session, now)
	if err != nil {
		t.Fatal(err)
	}
	return boundary, lifecycle
}
func roundCommand(data contracts.CollectionData, session contracts.BackendSessionID, operation string) contracts.CollectionCommandRequest {
	revision := data.Revision
	latest := data.Rounds[len(data.Rounds)-1]
	version := latest.RoundVersion
	return contracts.CollectionCommandRequest{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: session, OperationID: contracts.OperationID(operation), ExpectedRevision: &revision}, CollectionID: data.CollectionID, RoundID: latest.RoundID, ExpectedVersion: &version}
}
func requireCollectionOK(t *testing.T, response contracts.CollectionResponse, err error) contracts.CollectionData {
	t.Helper()
	if err != nil || !response.OK || response.Data == nil || response.Validate() != nil {
		t.Fatalf("collection envelope %+v/%v", response, err)
	}
	return *response.Data
}
func requireCollectionFailure(t *testing.T, response contracts.CollectionResponse, err error, code contracts.ErrorCode) {
	t.Helper()
	if err != nil || response.OK || response.Data != nil || response.Code != code || response.Validate() != nil {
		t.Fatalf("safe failure %+v/%v", response, err)
	}
}
func TestRoundBoundaryLocalSQLiteSelectionResultAndBootstrap(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "session-one", clock, true)
	entropy := new(roundBoundaryEntropy)
	boundary, _ := roundBoundary(t, store, draft, "session-one", clock, entropy)
	request := contracts.CreateCollectionRequest{DraftMutationHeader: roundDraftHeader(draft, "actual-create"), Mode: rl.ImmediateMode}
	response, err := boundary.CreateCollection(context.Background(), request)
	data := requireCollectionOK(t, response, err)
	if data.SelectedCount != 3 || data.ParticipantCount != 3 || data.RemainingCount != 2 || data.Article.GalleryName != "ÖGG 갤러리" || len(data.Rounds) != 1 || len(data.Rounds[0].Winners) != 1 || data.Rounds[0].State != contracts.Completed || data.Rounds[0].Winners[0].Participant.Nickname != "가" || data.Rounds[0].AppVersion != "desktop-test-version" {
		t.Fatal(data)
	}
	raw, _ := json.Marshal(response)
	for _, secret := range []string{"password-one", "\"Credential\"", "verifier_json", "\"Salt\"", "\"Key\"", "sqlite3"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret/internal metadata in IPC", secret)
		}
	}
	replay, err := boundary.CreateCollection(context.Background(), request)
	again := requireCollectionOK(t, replay, err)
	if again.CollectionID != data.CollectionID || again.Rounds[0].Winners[0].Participant.ID != data.Rounds[0].Winners[0].Participant.ID || entropy.calls.Load() != 1 {
		t.Fatal(again)
	}
	base, err := NewService("session-one", clock, store, RoundBootstrapSource(boundary))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := base.Bootstrap(context.Background())
	if err != nil || !bootstrap.OK || bootstrap.Data.ActiveDraft.State != contracts.DraftFinalized || len(bootstrap.Data.RecentResults) != 1 || len(bootstrap.Data.PendingOperations) != 0 {
		t.Fatal(bootstrap, err)
	}
	if bootstrap.Data.RecentResults[0].CollectionID != data.CollectionID || bootstrap.Data.RecentResults[0].RoundID != data.Rounds[0].RoundID || bootstrap.Data.RecentResults[0].Revision != data.Revision {
		t.Fatal("bootstrap lost the exact committed result reference", bootstrap.Data.RecentResults)
	}
	rerun := roundCommand(data, "session-one", "actual-rerun")
	rerun.Mode = rl.ImmediateMode
	rerun.Prizes = []contracts.PrizeInput{{ID: "rerun-prize", Name: "새 품목", Count: 1}}
	rerun.Message = "두 번째"
	nextResponse, err := boundary.Rerun(context.Background(), rerun)
	next := requireCollectionOK(t, nextResponse, err)
	if len(next.Rounds) != 2 || next.RemainingCount != 1 || next.Rounds[1].Winners[0].Participant.Nickname != "나" || entropy.calls.Load() != 2 {
		t.Fatal(next)
	}
}
func TestRoundBoundaryRestartAllowsLocalRerunAndPreservesReplayAndSessionFence(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "session-one", clock, true)
	entropy := new(roundBoundaryEntropy)
	boundary, _ := roundBoundary(t, store, draft, "session-one", clock, entropy)
	response, err := boundary.CreateCollection(context.Background(), contracts.CreateCollectionRequest{DraftMutationHeader: roundDraftHeader(draft, "create"), Mode: rl.ImmediateMode})
	data := requireCollectionOK(t, response, err)
	freshDraft := roundDraft(t, "session-two", clock, false)
	fresh, _ := roundBoundary(t, store, freshDraft, "session-two", clock, entropy)
	read, err := fresh.GetCollection(context.Background(), contracts.CollectionQuery{CollectionID: data.CollectionID})
	readData := requireCollectionOK(t, read, err)
	if len(readData.Rounds) != 1 || len(readData.Rounds[0].Winners) != 1 || readData.RemainingCount != 2 {
		t.Fatal("restart changed committed result", readData)
	}
	command := roundCommand(readData, "session-two", "new-session-rerun")
	command.Mode = rl.ImmediateMode
	command.Prizes = []contracts.PrizeInput{{ID: "new-prize", Count: 1}}
	next, err := fresh.Rerun(context.Background(), command)
	nextData := requireCollectionOK(t, next, err)
	if nextData.RemainingCount != 1 || len(nextData.Rounds) != 2 || entropy.calls.Load() != 2 {
		t.Fatal("local rerun did not execute once", nextData)
	}
	if nextData.Rounds[0].RoundID != readData.Rounds[0].RoundID || nextData.Rounds[0].Winners[0].Participant.ID != readData.Rounds[0].Winners[0].Participant.ID {
		t.Fatal("rerun changed prior winners")
	}
	replay, err := fresh.Rerun(context.Background(), command)
	replayData := requireCollectionOK(t, replay, err)
	if replayData.Revision != nextData.Revision || len(replayData.Rounds) != 2 || entropy.calls.Load() != 2 {
		t.Fatal("replay executed twice", replayData)
	}
	command.BackendSessionID = "session-one"
	stale, err := fresh.Rerun(context.Background(), command)
	requireCollectionFailure(t, stale, err, contracts.BackendSessionChanged)
	if entropy.calls.Load() != 2 {
		t.Fatal("stale session executed entropy")
	}
}
func TestRoundBoundaryQuickAndDirectScheduleCrossActualSQLiteBridge(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 123, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "session-one", clock, true)
	configuration, err := draft.Get(context.Background(), contracts.DraftQuery{BackendSessionID: "session-one", DraftID: draft.Summary().DraftID})
	if err != nil {
		t.Fatal(err)
	}
	prizes := configuration.Prizes
	prizes.DrawMode = rl.ReservationMode
	if _, err = draft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: roundDraftHeader(draft, "reservation-mode"), Kind: "SetPrizes", Prizes: &prizes}); err != nil {
		t.Fatal(err)
	}
	entropy := new(roundBoundaryEntropy)
	boundary, lifecycle := roundBoundary(t, store, draft, "session-one", clock, entropy)
	created, err := boundary.CreateCollection(context.Background(), contracts.CreateCollectionRequest{DraftMutationHeader: roundDraftHeader(draft, "reservation-create"), Mode: rl.ReservationMode})
	pending := requireCollectionOK(t, created, err)
	if pending.Rounds[0].State != contracts.PendingSchedule || entropy.calls.Load() != 0 {
		t.Fatal(pending)
	}
	request := roundCommand(pending, "session-one", "quick")
	delay := uint32(30)
	request.QuickDelaySeconds = &delay
	scheduledResponse, err := boundary.SetSchedule(context.Background(), request)
	scheduled := requireCollectionOK(t, scheduledResponse, err)
	if scheduled.Rounds[0].ScheduledAt == nil || !scheduled.Rounds[0].ScheduledAt.Equal(now.Add(30*time.Second)) {
		t.Fatal(scheduled)
	}
	cancelledResponse, err := boundary.CancelSchedule(context.Background(), roundCommand(scheduled, "session-one", "cancel"))
	cancelled := requireCollectionOK(t, cancelledResponse, err)
	if cancelled.Rounds[0].State != contracts.Cancelled || cancelled.RemainingCount != 3 {
		t.Fatal(cancelled)
	}
	rerun := roundCommand(cancelled, "session-one", "new-reservation")
	rerun.Mode = rl.ReservationMode
	rerun.Prizes = []contracts.PrizeInput{{ID: "new-prize", Count: 1}}
	newResponse, err := boundary.Rerun(context.Background(), rerun)
	newPending := requireCollectionOK(t, newResponse, err)
	direct := roundCommand(newPending, "session-one", "direct-time")
	at := now.Truncate(time.Minute).Add(time.Minute)
	direct.ScheduledAt = &at
	directResponse, err := boundary.SetSchedule(context.Background(), direct)
	directData := requireCollectionOK(t, directResponse, err)
	if !directData.Rounds[1].ScheduledAt.Equal(at) {
		t.Fatal(directData)
	}
	now = at
	due, err := lifecycle.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: directData.Rounds[1].RoundID})
	if err != nil || due.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(due, err)
	}
	read, err := boundary.GetCollection(context.Background(), contracts.CollectionQuery{CollectionID: pending.CollectionID})
	result := requireCollectionOK(t, read, err)
	if result.Rounds[1].State != contracts.Completed || result.Rounds[1].ExecutedAt == nil || !result.Rounds[1].ScheduledAt.Equal(at) || result.RemainingCount != 2 {
		t.Fatal(result)
	}
}
func TestRoundBoundaryFailedExecutionIsSafeEnvelopeAndStoredCollectionCanRetry(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "session-one", clock, true)
	entropy := new(roundBoundaryEntropy)
	entropy.fail.Store(true)
	boundary, _ := roundBoundary(t, store, draft, "session-one", clock, entropy)
	response, err := boundary.CreateCollection(context.Background(), contracts.CreateCollectionRequest{DraftMutationHeader: roundDraftHeader(draft, "failed-create"), Mode: rl.ImmediateMode})
	requireCollectionFailure(t, response, err, contracts.InvalidState)
	summary := draft.Summary()
	if summary.State != contracts.DraftFinalized || summary.CollectionID == nil {
		t.Fatal(summary)
	}
	read, err := boundary.GetCollection(context.Background(), contracts.CollectionQuery{CollectionID: *summary.CollectionID})
	failed := requireCollectionOK(t, read, err)
	if failed.Rounds[0].State != contracts.Failed || len(failed.Rounds[0].Winners) != 0 || failed.RemainingCount != 3 {
		t.Fatal(failed)
	}
	entropy.fail.Store(false)
	retried, err := boundary.RetryRound(context.Background(), roundCommand(failed, "session-one", "retry"))
	done := requireCollectionOK(t, retried, err)
	if done.Rounds[0].Attempt != 2 || len(done.Rounds[0].Winners) != 1 || entropy.calls.Load() != 2 {
		t.Fatal(done)
	}
}
