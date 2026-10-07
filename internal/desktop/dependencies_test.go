package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestDependencyNilKindsAndPresentValues(t *testing.T) {
	for _, value := range []interface{}{nil, (*int)(nil), (func())(nil), (chan int)(nil), (map[string]int)(nil), ([]int)(nil)} {
		if !nilDependency(value) {
			t.Fatalf("nil dependency accepted: %T", value)
		}
	}
	for _, value := range []interface{}{0, "", struct{}{}, new(int), func() {}, make(chan int), map[string]int{}, []int{}} {
		if nilDependency(value) {
			t.Fatalf("present dependency rejected: %T", value)
		}
	}
}

type pointerReader struct{}

func (*pointerReader) ReadOperation(context.Context, contracts.OperationID) (rl.OperationRecord, error) {
	panic("nil dependency dispatched")
}
func (*pointerReader) ListPendingOperations(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
	panic("nil dependency dispatched")
}

type functionReader func()

func (functionReader) ReadOperation(context.Context, contracts.OperationID) (rl.OperationRecord, error) {
	panic("nil dependency dispatched")
}
func (functionReader) ListPendingOperations(context.Context, contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
	panic("nil dependency dispatched")
}

func TestServiceConstructorsRejectTypedNilReaders(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }
	for _, reader := range []interface {
		OperationReader
		contracts.PendingOperationsReader
	}{(*pointerReader)(nil), functionReader(nil)} {
		if service, err := NewService("session", clock, reader); service != nil || err == nil {
			t.Fatal("typed nil pending reader accepted")
		}
		if service, err := NewRecoveryService("session", clock, reader); service != nil || err == nil {
			t.Fatal("typed nil operation reader accepted")
		}
	}
}
