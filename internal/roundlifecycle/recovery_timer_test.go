package roundlifecycle

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

type timerCommandPorts struct {
	*commandPorts
	hints atomic.Int32
	hint  func(context.Context) (bool, error)
}

func (p *timerCommandPorts) HasRecoveryWork(ctx context.Context) (bool, error) {
	p.hints.Add(1)
	return p.hint(ctx)
}

type hintWithoutLifecycleReader struct {
	Storage
	hints atomic.Int32
}

func (p *hintWithoutLifecycleReader) HasRecoveryWork(context.Context) (bool, error) {
	p.hints.Add(1)
	return false, nil
}

func TestRecoverTimerFalseTrueUnsupportedAndReaderCapabilityStaySeparate(t *testing.T) {
	for _, name := range []string{"no durable work", "durable work", "unsupported optional reader", "hint without lifecycle reader"} {
		t.Run(name, func(t *testing.T) {
			ports := &commandPorts{recoverable: func(context.Context) ([]RoundRecord, error) { return []RoundRecord{}, nil }}
			hint := &timerCommandPorts{commandPorts: ports, hint: func(context.Context) (bool, error) { return name == "durable work", nil }}
			var storage Storage = hint
			missing := &hintWithoutLifecycleReader{Storage: ports}
			if name == "unsupported optional reader" {
				storage = ports
			}
			if name == "hint without lifecycle reader" {
				storage = missing
			}
			service, e := newCommandService(t, storage)
			report, err := service.RecoverTimer(context.Background(), RecoverRequest{})
			if name == "hint without lifecycle reader" {
				timerFault(t, err, contracts.InvalidState)
				if missing.hints.Load() != 0 || !reflect.DeepEqual(report, RecoveryReport{}) {
					t.Fatal("unusable storage reader advanced to hint", report, err)
				}
				return
			}
			if err != nil || report.Completed == nil || report.Failed == nil || report.Due == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
				t.Fatal(report, err)
			}
			want := 0
			if name == "durable work" || name == "unsupported optional reader" {
				want = 1
			}
			if ports.counts()["recoverable"] != want {
				t.Fatal("wrong full Recover path", ports.counts())
			}
			if name != "unsupported optional reader" && hint.hints.Load() != 1 {
				t.Fatal("hint count", hint.hints.Load())
			}
			if e.entropy.calls.Load() != 0 || e.notices.Load() != 0 || e.ids.Load() != 0 {
				t.Fatal("timer hint performed mutation or secret work")
			}
		})
	}
}

func TestRecoverTimerContextAndHintErrorsFailClosedWithoutFullRead(t *testing.T) {
	for _, name := range []string{"nil context", "pre cancelled", "expired deadline", "closed service", "raw error false", "raw error true", "typed fault", "cancel error", "deadline error", "cancelled after false hint", "cancelled after true hint", "dependency error before cancellation"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ports := &commandPorts{}
			hint := &timerCommandPorts{commandPorts: ports}
			hint.hint = func(context.Context) (bool, error) {
				switch name {
				case "raw error false":
					return false, commandPortFailure
				case "raw error true":
					return true, commandPortFailure
				case "typed fault":
					return false, contracts.NewFault(contracts.InvalidState)
				case "cancel error":
					return false, context.Canceled
				case "deadline error":
					return false, context.DeadlineExceeded
				case "cancelled after false hint":
					cancel()
					return false, nil
				case "cancelled after true hint":
					cancel()
					return true, nil
				case "dependency error before cancellation":
					cancel()
					return false, commandPortFailure
				}
				return false, nil
			}
			service, e := newCommandService(t, hint)
			var queryContext context.Context = ctx
			switch name {
			case "nil context":
				queryContext = nil
			case "pre cancelled":
				cancel()
			case "expired deadline":
				var deadlineCancel context.CancelFunc
				queryContext, deadlineCancel = context.WithDeadline(context.Background(), time.Unix(0, 0))
				defer deadlineCancel()
			case "closed service":
				if err := service.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			report, err := service.RecoverTimer(queryContext, RecoverRequest{})
			if err == nil || !reflect.DeepEqual(report, RecoveryReport{}) {
				t.Fatal("hint error confirmed an empty report", report, err)
			}
			switch name {
			case "nil context":
				timerFault(t, err, contracts.InvalidInput)
			case "closed service", "typed fault":
				timerFault(t, err, contracts.InvalidState)
			case "raw error false", "raw error true", "dependency error before cancellation":
				timerFault(t, err, contracts.StorageUnavailable)
			case "pre cancelled", "cancel error", "cancelled after false hint", "cancelled after true hint":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "expired deadline", "deadline error":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			}
			if len(ports.counts()) != 0 || e.entropy.calls.Load() != 0 {
				t.Fatal("failed hint advanced to full reader or RNG", ports.counts())
			}
			wantHint := int32(1)
			if name == "nil context" || name == "pre cancelled" || name == "expired deadline" || name == "closed service" {
				wantHint = 0
			}
			if hint.hints.Load() != wantHint {
				t.Fatal("precondition advanced to hint", hint.hints.Load(), wantHint)
			}
		})
	}
}

func timerFault(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func awaitTimerClosed(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !service.closed.Load() {
		select {
		case <-ctx.Done():
			t.Fatal("Close did not cross its admission barrier")
		default:
			runtime.Gosched()
		}
	}
}

type timerContextHook struct {
	context.Context
	onErr func() error
}

func (ctx timerContextHook) Err() error { return ctx.onErr() }
func TestRecoverTimerCloseAfterBeginWorkBeforeHintRefusesWithoutRead(t *testing.T) {
	ports := &commandPorts{}
	hint := &timerCommandPorts{commandPorts: ports, hint: func(context.Context) (bool, error) { t.Fatal("closed service used hint"); return false, nil }}
	service, _ := newCommandService(t, hint)
	closeDone := make(chan error, 1)
	ctx := timerContextHook{Context: context.Background(), onErr: func() error {
		go func() { closeDone <- service.Close(context.Background()) }()
		awaitTimerClosed(t, service)
		return nil
	}}
	report, err := service.RecoverTimer(ctx, RecoverRequest{})
	timerFault(t, err, contracts.InvalidState)
	if !reflect.DeepEqual(report, RecoveryReport{}) || hint.hints.Load() != 0 {
		t.Fatal(report, hint.hints.Load())
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}
func TestRecoverTimerCloseDuringFalseOrTrueHintWaitsThenRejects(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "work"}[present], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered := make(chan struct{})
			release := make(chan struct{})
			ports := &commandPorts{}
			hint := &timerCommandPorts{commandPorts: ports, hint: func(ctx context.Context) (bool, error) {
				close(entered)
				select {
				case <-release:
					return present, nil
				case <-ctx.Done():
					return false, ctx.Err()
				}
			}}
			service, _ := newCommandService(t, hint)
			done := make(chan error, 1)
			go func() { _, err := service.RecoverTimer(ctx, RecoverRequest{}); done <- err }()
			<-entered
			closeDone := make(chan error, 1)
			go func() { closeDone <- service.Close(ctx) }()
			awaitTimerClosed(t, service)
			select {
			case err := <-closeDone:
				t.Fatal("Close did not wait for accepted hint", err)
			default:
			}
			close(release)
			timerFault(t, <-done, contracts.InvalidState)
			if err := <-closeDone; err != nil {
				t.Fatal(err)
			}
			if len(ports.counts()) != 0 || hint.hints.Load() != 1 {
				t.Fatal("close gap advanced to full recovery", ports.counts(), hint.hints.Load())
			}
		})
	}
}
func TestRecoverTimerDuplicateActiveHintsUseExistingSerialRecoveryGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var active, max atomic.Int32
	ports := &commandPorts{recoverable: func(ctx context.Context) ([]RoundRecord, error) {
		now := active.Add(1)
		if now > max.Load() {
			max.Store(now)
		}
		defer active.Add(-1)
		entered <- struct{}{}
		select {
		case <-release:
			return []RoundRecord{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	hint := &timerCommandPorts{commandPorts: ports, hint: func(context.Context) (bool, error) { return true, nil }}
	service, _ := newCommandService(t, hint)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := service.RecoverTimer(ctx, RecoverRequest{}); done <- err }()
	}
	<-entered
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if max.Load() != 1 || ports.counts()["recoverable"] != 2 || hint.hints.Load() != 2 {
		t.Fatal("timer duplicated concurrent preflight", max.Load(), ports.counts(), hint.hints.Load())
	}
}
