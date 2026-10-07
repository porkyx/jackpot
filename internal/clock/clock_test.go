package clock

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type controlledClock struct {
	makeTimer func(time.Duration) Timer
	calls     int
}

func (clock *controlledClock) Now() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }
func (clock *controlledClock) NewTimer(duration time.Duration) Timer {
	clock.calls++
	return clock.makeTimer(duration)
}

type controlledTimer struct {
	channel chan time.Time
	stops   atomic.Int32
	reads   int
	onRead  func()
}

func (timer *controlledTimer) C() <-chan time.Time {
	timer.reads++
	if timer.onRead != nil {
		timer.onRead()
	}
	return timer.channel
}
func (timer *controlledTimer) Stop() bool { return timer.stops.Add(1) == 1 }

func TestWaitValidatesIndependentArgumentsBeforeAllocatingTimer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		clock    Clock
		duration time.Duration
	}{
		{"nil context", nil, &controlledClock{}, time.Second}, {"nil clock", context.Background(), nil, time.Second},
		{"negative", context.Background(), &controlledClock{}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Wait(tc.ctx, tc.clock, tc.duration); err == nil {
				t.Fatal("invalid wait accepted")
			}
		})
	}
}

func TestWaitZeroAndPreCancelledAllocateNoTimer(t *testing.T) {
	clock := &controlledClock{}
	if err := Wait(context.Background(), clock, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx, clock, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Time{})
	defer stop()
	if err := Wait(deadline, clock, time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if clock.calls != 0 {
		t.Fatalf("allocated %d timers", clock.calls)
	}
}

func TestWaitBrokenTimerFailsAndCleansAcquiredResource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := &controlledTimer{onRead: cancel}
	for _, returned := range []Timer{nil, timer} {
		clock := &controlledClock{makeTimer: func(time.Duration) Timer { return returned }}
		if err := Wait(ctx, clock, time.Second); !errors.Is(err, ErrInvalidTimer) {
			t.Fatal("invalid timer accepted")
		}
	}
	if timer.stops.Load() != 1 || timer.reads != 1 {
		t.Fatalf("cleanup stops=%d reads=%d", timer.stops.Load(), timer.reads)
	}
}

func TestWaitControlledExpiryStopsTimerExactlyOnce(t *testing.T) {
	timer := &controlledTimer{channel: make(chan time.Time, 1)}
	timer.channel <- time.Time{}
	clock := &controlledClock{makeTimer: func(duration time.Duration) Timer {
		if duration != time.Second {
			t.Fatal(duration)
		}
		return timer
	}}
	if err := Wait(context.Background(), clock, time.Second); err != nil {
		t.Fatal(err)
	}
	if timer.stops.Load() != 1 || timer.reads != 1 {
		t.Fatalf("cleanup stops=%d reads=%d", timer.stops.Load(), timer.reads)
	}
}

func TestWaitDuringCancellationJoinsWorkerAndStopsTimer(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		timer := &controlledTimer{channel: make(chan time.Time)}
		admitted := make(chan struct{})
		done := make(chan error, 1)
		clock := &controlledClock{makeTimer: func(time.Duration) Timer { close(admitted); return timer }}
		go func() { done <- Wait(ctx, clock, time.Second) }()
		<-admitted
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if timer.stops.Load() != 1 {
			t.Fatalf("timer leaked at %d", i)
		}
	}
}

func TestWaitSimultaneousExpiryAndCancellationAlwaysReturnsCancellation(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		timer := &controlledTimer{channel: make(chan time.Time, 1)}
		clock := &controlledClock{makeTimer: func(time.Duration) Timer { timer.channel <- time.Time{}; cancel(); return timer }}
		if err := Wait(ctx, clock, time.Second); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if timer.stops.Load() != 1 {
			t.Fatal("timer leaked")
		}
	}
}

func TestSystemClockUTCAndTimerStopIdempotency(t *testing.T) {
	clock := System{}
	now := clock.Now()
	_, offset := now.Zone()
	if now.IsZero() || offset != 0 {
		t.Fatal("invalid system clock")
	}
	timer := clock.NewTimer(time.Hour)
	if timer.C() == nil || !timer.Stop() || timer.Stop() {
		t.Fatal("system timer lifecycle violated")
	}
}
