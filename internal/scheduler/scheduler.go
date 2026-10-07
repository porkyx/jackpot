// Package scheduler provides app-lifetime wake signals to RoundLifecycle.
// It never reads SQL, generates winners or retries failed attempts.
package scheduler

import (
	"context"
	"errors"
	"time"

	appclock "github.com/porkyx/jackpot/internal/clock"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type Lifecycle interface {
	Recover(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error)
}

// A lifecycle may avoid full recovery on idle timer polls. Startup and native
// wake signals always use Recover; the lifecycle owns the durable work check.
type timerLifecycle interface {
	RecoverTimer(context.Context, rl.RecoverRequest) (rl.RecoveryReport, error)
}

type wakeReason uint8

const (
	wakeTimer wakeReason = iota
	wakeSignal
	wakeClosed
)

func Run(ctx context.Context, clock appclock.Clock, lifecycle Lifecycle, report func(error)) error {
	return RunWithWake(ctx, clock, lifecycle, nil, report)
}

// RunWithWake checks durable state on timer ticks and coalesced OS resume signals.
func RunWithWake(ctx context.Context, clock appclock.Clock, lifecycle Lifecycle, wake <-chan struct{}, report func(error)) error {
	if ctx == nil || clock == nil || lifecycle == nil {
		return errors.New("invalid scheduler dependencies")
	}
	poller, timerPolling := lifecycle.(timerLifecycle)
	reason := wakeSignal
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if timerPolling && reason == wakeTimer {
			_, err = poller.RecoverTimer(ctx, rl.RecoverRequest{})
		} else {
			_, err = lifecycle.Recover(ctx, rl.RecoverRequest{})
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if report != nil {
				report(err)
			}
		}
		reason, err = waitForWake(ctx, clock, wake)
		if err != nil {
			return err
		}
		if reason == wakeClosed {
			wake = nil
		}
	}
}

// NewWakeSignal coalesces repeated native signals without blocking the UI thread.
func NewWakeSignal() (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	return wake, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func waitForWake(ctx context.Context, clock appclock.Clock, wake <-chan struct{}) (wakeReason, error) {
	if err := ctx.Err(); err != nil {
		return wakeTimer, err
	}
	timer := clock.NewTimer(time.Second)
	if timer == nil {
		return wakeTimer, appclock.ErrInvalidTimer
	}
	defer timer.Stop()
	channel := timer.C()
	if channel == nil {
		return wakeTimer, appclock.ErrInvalidTimer
	}
	select {
	case <-ctx.Done():
		return wakeTimer, ctx.Err()
	case <-channel:
		return wakeTimer, ctx.Err()
	case _, open := <-wake:
		if !open {
			return wakeClosed, ctx.Err()
		}
		return wakeSignal, ctx.Err()
	}
}
