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

func TestOSWakeInterruptsOwnedTimerAndRecoveryRemainsSequential(t *testing.T) {
	clock := testSchedulerClock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	life := lifecycleFunc(func(ctx context.Context, _ rl.RecoverRequest) (rl.RecoveryReport, error) {
		if calls.Add(1) == 2 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return rl.RecoveryReport{}, ctx.Err()
			}
		}
		return rl.RecoveryReport{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- RunWithWake(ctx, clock, life, wake, nil) }()
	first := awaitWakeTimer(t, ctx, clock, done)
	wake <- struct{}{}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("OS wake did not interrupt pending timer")
	}
	if first.stops.Load() != 1 || calls.Load() != 2 {
		t.Fatal("wake duplicated recovery or leaked timer", calls.Load(), first.stops.Load())
	}
	close(release)
	awaitWakeTimer(t, ctx, clock, done)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertTimersStoppedOnce(t, clock)
}

func TestWakeSignalCoalescesConcurrentDuplicatesAndCanSignalAgain(t *testing.T) {
	wake, signal := NewWakeSignal()
	var group sync.WaitGroup
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() { defer group.Done(); signal() }()
	}
	group.Wait()
	if len(wake) != 1 {
		t.Fatal("duplicate wake signals were not coalesced", len(wake))
	}
	<-wake
	select {
	case <-wake:
		t.Fatal("duplicate signal survived")
	default:
	}
	signal()
	if len(wake) != 1 {
		t.Fatal("consumed wake could not be signalled again")
	}
	<-wake
}

func TestClosedWakeIsDisabledWithoutSpinningAndOwnedTimerStops(t *testing.T) {
	clock := testSchedulerClock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wake := make(chan struct{})
	close(wake)
	var calls atomic.Int32
	life := lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
		calls.Add(1)
		return rl.RecoveryReport{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- RunWithWake(ctx, clock, life, wake, nil) }()
	awaitWakeTimer(t, ctx, clock, done)
	awaitWakeTimer(t, ctx, clock, done)
	if calls.Load() != 2 {
		t.Fatal("closed signal caused duplicate recovery", calls.Load())
	}
	select {
	case <-clock.created:
		t.Fatal("closed signal spins")
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertTimersStoppedOnce(t, clock)
}

func TestWakeRecoveryFailureIsReportedOnceAndNextSignalAllowsRetry(t *testing.T) {
	for _, failure := range []string{"first", "second", "continuous"} {
		t.Run(failure, func(t *testing.T) {
			clock := testSchedulerClock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			wake, signal := NewWakeSignal()
			var calls, reports atomic.Int32
			life := lifecycleFunc(func(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error) {
				call := calls.Add(1)
				if failure == "continuous" || failure == "first" && call == 1 || failure == "second" && call == 2 {
					return rl.RecoveryReport{}, errors.New("dependency failed")
				}
				return rl.RecoveryReport{}, nil
			})
			done := make(chan error, 1)
			go func() { done <- RunWithWake(ctx, clock, life, wake, func(error) { reports.Add(1) }) }()
			for i := 0; i < 3; i++ {
				select {
				case <-clock.created:
				case <-ctx.Done():
					t.Fatal("wake recovery did not finish")
				}
				if calls.Load() != int32(i+1) {
					t.Fatal("wake recovery ordering", calls.Load())
				}
				if i < 2 {
					signal()
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
			if reports.Load() != want {
				t.Fatal("wake failure reporting", reports.Load(), want)
			}
			assertTimersStoppedOnce(t, clock)
		})
	}
}

func awaitWakeTimer(t *testing.T, ctx context.Context, clock *schedulerClock, done <-chan error) *schedulerTimer {
	t.Helper()
	select {
	case timer := <-clock.created:
		return timer
	case err := <-done:
		t.Fatal("scheduler stopped before owned timer", err)
	case <-ctx.Done():
		t.Fatal("scheduler did not create owned timer", ctx.Err())
	}
	return nil
}
