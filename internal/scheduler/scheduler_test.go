package scheduler

import (
	"context"

	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/contracts"

	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

type schedulerTimer struct {
	channel chan time.Time
	stops   atomic.Int32
}

func (timer *schedulerTimer) C() <-chan time.Time { return timer.channel }
func (timer *schedulerTimer) Stop() bool          { timer.stops.Add(1); return true }

type schedulerClock struct {
	mu         sync.Mutex
	now        time.Time
	created    chan *schedulerTimer
	timers     []*schedulerTimer
	invalid    bool
	nilChannel bool
}

func (clock *schedulerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}
func (clock *schedulerClock) Set(now time.Time) { clock.mu.Lock(); clock.now = now; clock.mu.Unlock() }
func (clock *schedulerClock) NewTimer(duration time.Duration) appclock.Timer {
	if duration != time.Second {
		panic("scheduler timer interval changed")
	}
	if clock.invalid {
		return nil
	}
	timer := &schedulerTimer{channel: make(chan time.Time, 1)}
	if clock.nilChannel {
		timer.channel = nil
	}
	clock.mu.Lock()
	clock.timers = append(clock.timers, timer)
	clock.mu.Unlock()
	clock.created <- timer
	return timer
}

type lifecycleFunc func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error)

func (function lifecycleFunc) Recover(ctx context.Context, request rl.RecoverRequest) (rl.RecoveryReport, error) {
	return function(ctx, request)
}
func testSchedulerClock() *schedulerClock {
	return &schedulerClock{now: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), created: make(chan *schedulerTimer, 8)}
}
func assertTimersStoppedOnce(t *testing.T, clock *schedulerClock) {
	t.Helper()
	clock.mu.Lock()
	defer clock.mu.Unlock()
	for _, timer := range clock.timers {
		if timer.stops.Load() != 1 {
			t.Fatalf("timer stop count=%d", timer.stops.Load())
		}
	}
}
func TestSchedulerRejectsMissingDependenciesAndPreCancelledRunWithoutTimers(t *testing.T) {
	clock := testSchedulerClock()
	lifecycle := lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
		t.Fatal("unexpected recovery")
		return rl.RecoveryReport{}, nil
	})
	for _, entry := range []struct {
		ctx   context.Context
		clock appclock.Clock
		life  Lifecycle
	}{{nil, clock, lifecycle}, {context.Background(), nil, lifecycle}, {context.Background(), clock, nil}} {
		if err := Run(entry.ctx, entry.clock, entry.life, nil); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, clock, lifecycle, nil); !errors.Is(err, context.Canceled) || len(clock.timers) != 0 {
		t.Fatal(err)
	}
}
func TestSchedulerReportsFirstNthContinuousErrorsAndKeepsOneTimer(t *testing.T) {
	for _, failure := range []string{"first", "second", "continuous"} {
		t.Run(failure, func(t *testing.T) {
			clock := testSchedulerClock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			var reports atomic.Int32
			lifecycle := lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
				call := calls.Add(1)
				if failure == "continuous" || (failure == "first" && call == 1) || (failure == "second" && call == 2) {
					return rl.RecoveryReport{}, errors.New("safe recovery failed")
				}
				return rl.RecoveryReport{}, nil
			})
			done := make(chan error, 1)
			go func() { done <- Run(ctx, clock, lifecycle, func(error) { reports.Add(1) }) }()
			for index := 0; index < 3; index++ {
				timer := <-clock.created
				if calls.Load() != int32(index+1) {
					t.Fatal("recovery/timer ordering")
				}
				if index < 2 {
					timer.channel <- clock.Now()
				}
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			want := int32(1)
			if failure == "continuous" {
				want = 3
			}
			if reports.Load() != want || len(clock.timers) != 3 {
				t.Fatal(reports.Load(), len(clock.timers))
			}
			assertTimersStoppedOnce(t, clock)
		})
	}
}
func TestSchedulerCancellationDuringRecoveryAndInvalidTimersCleanResources(t *testing.T) {
	for _, failure := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		clock := testSchedulerClock()
		reported := false
		err := Run(ctx, clock, lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
			cancel()
			if failure {
				return rl.RecoveryReport{}, errors.New("cancelled recovery")
			}
			return rl.RecoveryReport{}, nil
		}), func(error) { reported = true })
		if !errors.Is(err, context.Canceled) || reported || len(clock.timers) != 0 {
			t.Fatal(err, reported)
		}
	}
	for _, nilChannel := range []bool{false, true} {
		clock := testSchedulerClock()
		clock.invalid = !nilChannel
		clock.nilChannel = nilChannel
		err := Run(context.Background(), clock, lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
			return rl.RecoveryReport{}, errors.New("recovery error without observer")
		}), nil)
		if !errors.Is(err, appclock.ErrInvalidTimer) {
			t.Fatal(err)
		}
		assertTimersStoppedOnce(t, clock)
	}
}

type schedulerEntropy struct{ calls atomic.Int32 }

func (entropy *schedulerEntropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	entropy.calls.Add(1)
	return 0, ctx.Err()
}
func TestSchedulerActualSQLiteHandlesBackwardAndForwardClockThenDueExactlyOnce(t *testing.T) {
	clock := testSchedulerClock()
	start := clock.Now()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "scheduler.sqlite3"))
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
	var ids atomic.Uint32
	entropy := new(schedulerEntropy)
	service, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: "scheduler-session", Entropy: entropy, Clock: clock.Now, NewID: func() string { return fmt.Sprintf("scheduler-id-%d", ids.Add(1)) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	request := rl.CreateCollectionRequest{OperationID: "scheduled-create", Context: contracts.DraftContext{BackendSessionID: "scheduler-session", DraftID: "draft", Revision: 3, ArticleGeneration: 1}, Collection: rl.FrozenCollection{SourceDraftID: "draft", FinalizedDraftRevision: 3, ArticleGeneration: 1, Snapshot: contracts.SnapshotSummary{SnapshotID: "snapshot", CollectedAt: start, Complete: true}, Participants: []rl.ParticipantSnapshot{{ID: "one", Included: true}, {ID: "two", Included: true}}}, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"one", "two"}, Prizes: []rl.Prize{{ID: "prize", Count: 1}}, Mode: rl.ReservationMode}}
	pending, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	delay := uint32(30)
	scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "schedule", Context: contracts.RoundContext{CollectionContext: contracts.CollectionContext{BackendSessionID: "scheduler-session", CollectionID: pending.CollectionID, Revision: pending.Revision}, RoundID: pending.ID, Version: pending.Version}, QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	reports := make(chan error, 8)
	go func() { done <- Run(ctx, clock, service, func(err error) { reports <- err }) }()
	first := <-clock.created
	clock.Set(start.Add(-time.Hour))
	first.channel <- clock.Now()
	second := <-clock.created
	before, err := store.ReadRound(context.Background(), scheduled.ID)
	if err != nil || before.State != contracts.Scheduled || entropy.calls.Load() != 0 {
		t.Fatal(before, err)
	}
	clock.Set(start.Add(2 * time.Hour))
	second.channel <- clock.Now()
	third := <-clock.created
	completed, err := store.ReadRound(context.Background(), scheduled.ID)
	if err != nil || completed.State != contracts.Completed || completed.Outcome == nil || !completed.Outcome.ExecutedAt.Equal(clock.Now()) || !completed.ScheduledAt.Equal(start.Add(30*time.Second)) || entropy.calls.Load() != 1 {
		t.Fatal(completed, err)
	}
	third.channel <- clock.Now()
	<-clock.created
	if entropy.calls.Load() != 1 {
		t.Fatal("duplicate wake consumed entropy")
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatal(<-reports)
	}
	assertTimersStoppedOnce(t, clock)
}
