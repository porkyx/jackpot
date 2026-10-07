package clock

import (
	"context"
	"errors"
	"time"
)

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

var ErrInvalidTimer = errors.New("clock returned an invalid timer")

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}
type System struct{}

func (System) Now() time.Time                        { return time.Now().UTC() }
func (System) NewTimer(duration time.Duration) Timer { return systemTimer{time.NewTimer(duration)} }

type systemTimer struct{ timer *time.Timer }

func (timer systemTimer) C() <-chan time.Time { return timer.timer.C }
func (timer systemTimer) Stop() bool          { return timer.timer.Stop() }

// Wait owns its timer. Cancellation never leaves a timer or worker behind.
func Wait(ctx context.Context, clock Clock, duration time.Duration) error {
	if ctx == nil || clock == nil || duration < 0 {
		return errors.New("invalid clock wait")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if duration == 0 {
		return nil
	}
	timer := clock.NewTimer(duration)
	if timer == nil {
		return ErrInvalidTimer
	}
	defer timer.Stop()
	channel := timer.C()
	if channel == nil {
		return ErrInvalidTimer
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-channel:
		return ctx.Err()
	}
}
