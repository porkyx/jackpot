package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type timerPollLifecycle struct {
	full func(context.Context) error
	poll func(context.Context) error
}

func (life timerPollLifecycle) Recover(ctx context.Context, _ rl.RecoverRequest) (rl.RecoveryReport, error) {
	return rl.RecoveryReport{}, life.full(ctx)
}
func (life timerPollLifecycle) RecoverTimer(ctx context.Context, _ rl.RecoverRequest) (rl.RecoveryReport, error) {
	return rl.RecoveryReport{}, life.poll(ctx)
}

func startPollScheduler(t *testing.T, life Lifecycle, wake <-chan struct{}, report func(error)) (context.Context, *schedulerClock, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	clock := testSchedulerClock()
	done := make(chan error, 1)
	go func() { done <- RunWithWake(ctx, clock, life, wake, report) }()
	t.Cleanup(func() {
		cancel()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("scheduler cleanup: %v", err)
			}
		case <-timer.C:
			t.Error("scheduler did not stop after cancellation")
		}
		assertTimersStoppedOnce(t, clock)
	})
	return ctx, clock, done
}

func TestTimerPollStartupAndOSWakeAlwaysUseFullRecovery(t *testing.T) {
	wake, signal := NewWakeSignal()
	var full, poll atomic.Int32
	life := timerPollLifecycle{
		full: func(context.Context) error { full.Add(1); return nil },
		poll: func(context.Context) error { poll.Add(1); return nil },
	}
	ctx, clock, done := startPollScheduler(t, life, wake, nil)
	timer := awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 1 || poll.Load() != 0 {
		t.Fatal("startup skipped full recovery")
	}
	timer.channel <- clock.Now()
	timer = awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 1 || poll.Load() != 1 {
		t.Fatal("timer did not select lifecycle poll")
	}
	signal()
	timer = awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 2 || poll.Load() != 1 {
		t.Fatal("OS wake used timer-only recovery")
	}
	timer.channel <- clock.Now()
	awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 2 || poll.Load() != 2 {
		t.Fatal("wake changed later timer routing")
	}
}

func TestTimerPollUnsupportedLifecycleKeepsFullRecoveryOnEveryTick(t *testing.T) {
	var full atomic.Int32
	life := lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
		full.Add(1)
		return rl.RecoveryReport{}, nil
	})
	ctx, clock, done := startPollScheduler(t, life, nil, nil)
	for i := 1; i <= 3; i++ {
		timer := awaitWakeTimer(t, ctx, clock, done)
		if full.Load() != int32(i) {
			t.Fatal("unsupported lifecycle lost full recovery", full.Load())
		}
		if i < 3 {
			timer.channel <- clock.Now()
		}
	}
}

func TestTimerPollClosedWakeIsDisabledAndNextTickStillUsesPoll(t *testing.T) {
	wake := make(chan struct{})
	close(wake)
	var full, poll atomic.Int32
	life := timerPollLifecycle{
		full: func(context.Context) error { full.Add(1); return nil },
		poll: func(context.Context) error { poll.Add(1); return nil },
	}
	ctx, clock, done := startPollScheduler(t, life, wake, nil)
	awaitWakeTimer(t, ctx, clock, done)
	timer := awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 2 || poll.Load() != 0 {
		t.Fatal("closed signal changed full recovery compatibility")
	}
	select {
	case <-clock.created:
		t.Fatal("closed wake spins")
	default:
	}
	timer.channel <- clock.Now()
	awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 2 || poll.Load() != 1 {
		t.Fatal("closed channel was not disabled")
	}
}

func TestTimerPollFirstNthContinuousFailuresReportWithoutFullFallback(t *testing.T) {
	for _, failure := range []string{"first", "second", "continuous"} {
		t.Run(failure, func(t *testing.T) {
			var full, poll, reports atomic.Int32
			injected := errors.New("poll storage unavailable")
			life := timerPollLifecycle{
				full: func(context.Context) error { full.Add(1); return nil },
				poll: func(context.Context) error {
					call := poll.Add(1)
					if failure == "continuous" || failure == "first" && call == 1 || failure == "second" && call == 2 {
						return injected
					}
					return nil
				},
			}
			ctx, clock, done := startPollScheduler(t, life, nil, func(err error) {
				if !errors.Is(err, injected) {
					t.Error("changed reported error", err)
				}
				reports.Add(1)
			})
			timer := awaitWakeTimer(t, ctx, clock, done)
			for i := 1; i <= 3; i++ {
				timer.channel <- clock.Now()
				timer = awaitWakeTimer(t, ctx, clock, done)
				if full.Load() != 1 || poll.Load() != int32(i) {
					t.Fatal("poll failure retried or fell back to full recovery")
				}
			}
			want := int32(1)
			if failure == "continuous" {
				want = 3
			}
			if reports.Load() != want {
				t.Fatal("poll failure report count", reports.Load(), want)
			}
		})
	}
}

func TestTimerPollFailureWithNoObserverContinuesAtNextTick(t *testing.T) {
	var full, poll atomic.Int32
	life := timerPollLifecycle{
		full: func(context.Context) error { full.Add(1); return nil },
		poll: func(context.Context) error { poll.Add(1); return errors.New("poll failed") },
	}
	ctx, clock, done := startPollScheduler(t, life, nil, nil)
	for i := 0; i < 2; i++ {
		timer := awaitWakeTimer(t, ctx, clock, done)
		timer.channel <- clock.Now()
	}
	awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 1 || poll.Load() != 2 {
		t.Fatal("nil observer changed bounded tick behavior")
	}
}

func TestTimerPollCancellationDuringPollSuppressesErrorReportAndNextTimer(t *testing.T) {
	for _, dependencyError := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[dependencyError], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			clock := testSchedulerClock()
			var full, poll, reports atomic.Int32
			life := timerPollLifecycle{
				full: func(context.Context) error { full.Add(1); return nil },
				poll: func(context.Context) error {
					poll.Add(1)
					cancel()
					if dependencyError {
						return errors.New("dependency cancelled")
					}
					return nil
				},
			}
			done := make(chan error, 1)
			go func() { done <- Run(ctx, clock, life, func(error) { reports.Add(1) }) }()
			timer := awaitWakeTimer(t, ctx, clock, done)
			timer.channel <- clock.Now()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation not preserved", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled poll did not stop")
			}
			if full.Load() != 1 || poll.Load() != 1 || reports.Load() != 0 || len(clock.timers) != 1 {
				t.Fatal("cancellation leaked report, retry or timer")
			}
			assertTimersStoppedOnce(t, clock)
		})
	}
}

func TestTimerPollQueuedDuplicateOSWakeWaitsForPollAndRunsFullRecoveryOnce(t *testing.T) {
	wake, signal := NewWakeSignal()
	entered, release := make(chan struct{}), make(chan struct{})
	var full, poll, active, maximum atomic.Int32
	enter := func() {
		value := active.Add(1)
		for old := maximum.Load(); value > old && !maximum.CompareAndSwap(old, value); old = maximum.Load() {
		}
	}
	life := timerPollLifecycle{
		full: func(context.Context) error { enter(); defer active.Add(-1); full.Add(1); return nil },
		poll: func(ctx context.Context) error {
			enter()
			defer active.Add(-1)
			poll.Add(1)
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	ctx, clock, done := startPollScheduler(t, life, wake, nil)
	timer := awaitWakeTimer(t, ctx, clock, done)
	timer.channel <- clock.Now()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("poll did not enter")
	}
	var group sync.WaitGroup
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() { defer group.Done(); signal() }()
	}
	group.Wait()
	if full.Load() != 1 || poll.Load() != 1 || len(wake) != 1 || maximum.Load() != 1 {
		t.Fatal("queued wake overlapped active poll")
	}
	close(release)
	awaitWakeTimer(t, ctx, clock, done)
	awaitWakeTimer(t, ctx, clock, done)
	if full.Load() != 2 || poll.Load() != 1 || maximum.Load() != 1 || active.Load() != 0 {
		t.Fatal("wake duplicated execution or lost full recovery")
	}
}
