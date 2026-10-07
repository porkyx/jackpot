package sqlite

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type executionPairActor struct{}
type dueRecoveryStorage struct {
	*Store
	target   contracts.RoundID
	entered  chan string
	releases map[string]chan struct{}
	mu       sync.Mutex
	seen     map[string]bool
}

func (storage *dueRecoveryStorage) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {
	round, err := storage.Store.ReadRound(ctx, id)
	actor, _ := ctx.Value(executionPairActor{}).(string)
	if err == nil && id == storage.target && round.State == contracts.Scheduled && actor != "" {
		storage.mu.Lock()
		first := !storage.seen[actor]
		storage.seen[actor] = true
		storage.mu.Unlock()
		if first {
			storage.entered <- actor
			select {
			case <-storage.releases[actor]:
			case <-ctx.Done():
				return rl.RoundRecord{}, ctx.Err()
			}
		}
	}
	return round, err
}
func TestRoundEntryDueAndRecoveryCaptureSameScheduleAndEachClaimOrderExecutesOnce(t *testing.T) {
	for _, first := range []string{"due", "recovery"} {
		t.Run(first+" claim first", func(t *testing.T) {
			store := migratedTestStore(t)
			ports := &dueRecoveryStorage{Store: store, entered: make(chan string, 2), releases: map[string]chan struct{}{"due": make(chan struct{}), "recovery": make(chan struct{})}, seen: map[string]bool{}}
			entropy := new(productEntropy)
			now := storageNow
			service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return now })
			pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			delay := uint32(10)
			scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "schedule-pair", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
			if err != nil {
				t.Fatal(err)
			}
			ports.target = scheduled.ID
			now = *scheduled.ScheduledAt
			entropy.entered = make(chan struct{}, 1)
			entropy.release = make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type answer struct {
				actor  string
				round  rl.RoundRecord
				report rl.RecoveryReport
				err    error
			}
			answers := make(chan answer, 2)
			finished := make(chan struct{})
			var workers sync.WaitGroup
			workers.Add(2)
			var dueRelease, recoverRelease, entropyRelease sync.Once
			defer func() {
				dueRelease.Do(func() { close(ports.releases["due"]) })
				recoverRelease.Do(func() { close(ports.releases["recovery"]) })
				entropyRelease.Do(func() { close(entropy.release) })
				cancel()
				cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cleanupCancel()
				select {
				case <-finished:
				case <-cleanup.Done():
					t.Error("entry pair goroutines were not cleaned up")
				}
			}()
			go func() {
				defer workers.Done()
				round, err := service.ExecuteDue(context.WithValue(ctx, executionPairActor{}, "due"), rl.ExecuteDueRequest{RoundID: scheduled.ID})
				answers <- answer{actor: "due", round: round, err: err}
			}()
			go func() {
				defer workers.Done()
				report, err := service.Recover(context.WithValue(ctx, executionPairActor{}, "recovery"), rl.RecoverRequest{})
				answers <- answer{actor: "recovery", report: report, err: err}
			}()
			go func() { workers.Wait(); close(finished) }()
			captured := map[string]bool{}
			for index := 0; index < 2; index++ {
				select {
				case actor := <-ports.entered:
					captured[actor] = true
				case <-ctx.Done():
					t.Fatal("entry snapshots did not reach barriers", ctx.Err())
				}
			}
			if !captured["due"] || !captured["recovery"] {
				t.Fatal(captured)
			}
			if first == "due" {
				dueRelease.Do(func() { close(ports.releases["due"]) })
			} else {
				recoverRelease.Do(func() { close(ports.releases["recovery"]) })
			}
			select {
			case <-entropy.entered:
			case <-ctx.Done():
				t.Fatal("chosen actor did not claim", ctx.Err())
			}
			before := corruptionDurableSnapshot(t, store)
			if first == "due" {
				recoverRelease.Do(func() { close(ports.releases["recovery"]) })
			} else {
				dueRelease.Do(func() { close(ports.releases["due"]) })
			}
			var loser answer
			select {
			case loser = <-answers:
			case <-ctx.Done():
				t.Fatal("duplicate entry did not finish", ctx.Err())
			}
			if loser.actor == first || loser.err != nil || len(loser.report.Failed) != 0 || entropy.calls.Load() != 1 || before != corruptionDurableSnapshot(t, store) {
				t.Fatal("duplicate entry altered the active owner", loser, entropy.calls.Load())
			}
			if loser.actor == "due" && (loser.round.State != contracts.Executing || loser.round.Outcome != nil) {
				t.Fatal("duplicate due published an uncommitted result", loser)
			}
			if loser.actor == "recovery" && (len(loser.report.Completed) != 0 || len(loser.report.Due) != 1) {
				t.Fatal("duplicate recovery fabricated completion", loser)
			}
			entropyRelease.Do(func() { close(entropy.release) })
			var winner answer
			select {
			case winner = <-answers:
			case <-ctx.Done():
				t.Fatal("claim owner did not finish", ctx.Err())
			}
			if winner.actor != first || winner.err != nil {
				t.Fatal(winner)
			}
			if first == "due" && winner.round.State != contracts.Completed {
				t.Fatal(winner)
			}
			if first == "recovery" && (len(winner.report.Completed) != 1 || winner.report.Completed[0] != scheduled.ID || len(winner.report.Due) != 1) {
				t.Fatal(winner)
			}
			completed, err := store.ReadRound(context.Background(), scheduled.ID)
			if err != nil || completed.State != contracts.Completed || completed.Outcome == nil || completed.Outcome.Winners[0].ParticipantID != "p1" || entropy.calls.Load() != 1 || roundTableCount(t, store, "operations") != 3 || roundTableCount(t, store, "round_attempts") != 1 || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
				t.Fatal("execution pair lost uniqueness", completed, err)
			}
			unchanged := corruptionDurableSnapshot(t, store)
			if _, err := service.Recover(context.Background(), rl.RecoverRequest{}); err != nil {
				t.Fatal(err)
			}
			replay, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.ID})
			if err != nil || !reflect.DeepEqual(replay, completed) || unchanged != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
				t.Fatal("post-pair wake repeated draw", replay, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestRoundEntryCreateRerunRetryAndDueLiveClaimSurvivesTimerRecoveryAndReplay(t *testing.T) {
	for _, kind := range []string{"create", "rerun", "retry", "due"} {
		t.Run(kind, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			now := storageNow
			service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
			var input rl.RoundRecord
			var admission rl.Admission
			var err error
			if kind == "create" {
				request := productRequest(now, rl.ImmediateMode)
				prepared, prepareErr := service.PrepareCreate(context.Background(), request)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer prepared.Close()
				admission, err = service.AdmitCreate(context.Background(), prepared, request.Collection)
				if err != nil {
					t.Fatal(err)
				}
				input = admission.Round
			} else {
				mode := rl.ImmediateMode
				if kind == "due" {
					mode = rl.ReservationMode
				}
				if kind == "retry" {
					entropy.fail.Store(true)
				}
				input, err = service.CreateCollection(context.Background(), productRequest(now, mode))
				if kind == "retry" {
					requireFault(t, err, contracts.InvalidState)
				} else if err != nil {
					t.Fatal(err)
				}
				entropy.fail.Store(false)
				if kind == "due" {
					delay := uint32(10)
					input, err = service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "live-schedule", Context: productRoundContext(input, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
					if err != nil {
						t.Fatal(err)
					}
					now = *input.ScheduledAt
				}
			}
			entropy.entered = make(chan struct{}, 1)
			entropy.release = make(chan struct{})
			var released sync.Once
			done := make(chan roundCommandResult, 1)
			requestContext := contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: input.CollectionID, Revision: input.Revision}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			returned := false
			defer func() {
				released.Do(func() { close(entropy.release) })
				cancel()
				if !returned {
					cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cleanupCancel()
					select {
					case <-done:
					case <-cleanup.Done():
						t.Error("live owner goroutine cleanup failed")
					}
				}
			}()
			go func() {
				var result rl.RoundRecord
				var err error
				switch kind {
				case "create":
					result, err = service.ExecuteAdmission(ctx, admission)
				case "rerun":
					result, err = service.Rerun(ctx, rl.RerunRequest{OperationID: "live-rerun", Context: requestContext, Prizes: []rl.Prize{{ID: "next", Count: 1}}, Mode: rl.ImmediateMode})
				case "retry":
					result, err = service.RetryRound(ctx, rl.RetryRequest{OperationID: "live-retry", Context: productRoundContext(input, "session-one")})
				case "due":
					result, err = service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: input.ID})
				}
				done <- roundCommandResult{result, err}
			}()
			select {
			case <-entropy.entered:
			case <-ctx.Done():
				t.Fatal("primary entry did not claim", ctx.Err())
			}
			rounds, err := store.ListRounds(ctx, input.CollectionID)
			if err != nil {
				t.Fatal(err)
			}
			active := rounds[len(rounds)-1]
			if active.State != contracts.Executing || active.Claim == nil {
				t.Fatal("owner has no durable claim", active)
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			report, err := service.Recover(ctx, rl.RecoverRequest{})
			if err != nil || len(report.Failed) != 0 || len(report.Due) != 0 || len(report.Completed) != 0 {
				t.Fatal("recovery stole live claim", report, err)
			}
			observer, err := service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: active.ID})
			if err != nil || observer.State != contracts.Executing || observer.Outcome != nil {
				t.Fatal("timer changed user/live execution", observer, err)
			}
			if kind == "rerun" {
				replay, err := service.Rerun(ctx, rl.RerunRequest{OperationID: "live-rerun", Context: requestContext, Prizes: []rl.Prize{{ID: "next", Count: 1}}, Mode: rl.ImmediateMode})
				if err != nil || replay.State != contracts.Executing || replay.Outcome != nil {
					t.Fatal(replay, err)
				}
			}
			if kind == "retry" {
				replay, err := service.RetryRound(ctx, rl.RetryRequest{OperationID: "live-retry", Context: productRoundContext(input, "session-one")})
				if err != nil || replay.State != contracts.Executing || replay.Outcome != nil {
					t.Fatal(replay, err)
				}
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("observer replay changed durable execution")
			}
			released.Do(func() { close(entropy.release) })
			var result roundCommandResult
			select {
			case result = <-done:
				returned = true
			case <-ctx.Done():
				t.Fatal("primary execution did not finish", ctx.Err())
			}
			if result.err != nil || result.round.State != contracts.Completed || result.round.Outcome == nil || entropy.calls.Load() != calls {
				t.Fatal(result, entropy.calls.Load())
			}
			expectedResults := 1
			if kind == "rerun" {
				expectedResults = 2
			}
			if roundTableCount(t, store, "results") != expectedResults || roundTableCount(t, store, "winners") != expectedResults {
				t.Fatal("entry observers duplicated results")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
