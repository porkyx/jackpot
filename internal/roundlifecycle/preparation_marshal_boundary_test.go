package roundlifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

// A synchronous typed dependency can mutate the caller's still-shared pointee
// after canonicalCreate succeeds. No concurrent caller or data race is needed.
func TestPrepareCreateCollectionMarshalAfterTypedReadMutationRefusesAndReleasesWork(t *testing.T) {
	request := boundaryCreateRequest()
	postedAt := commandTime()
	request.Collection.Article.PostedAt = &postedAt
	mutate := true
	ports := &commandPorts{operation: func(_ context.Context, id contracts.OperationID) (OperationRecord, error) {
		if mutate {
			postedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		return OperationRecord{Operation: OperationIdentity{ID: id}, Status: contracts.OperationUnknown}, nil
	}}
	service, effects := newCommandService(t, ports)

	prepared, err := service.PrepareCreate(context.Background(), request)
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if prepared != nil || postedAt.Year() != 10000 {
		t.Fatal("post-read invalid timestamp escaped as a prepared snapshot", prepared, err)
	}
	assertCommandCalls(t, ports, map[string]int{"operation": 1, "find": 1})
	if effects.ids.Load() != 0 || effects.entropy.calls.Load() != 0 || effects.notices.Load() != 0 {
		t.Fatal("failed ownership serialization generated IDs, RNG, or notices")
	}
	service.mu.Lock()
	active := len(service.admittedRounds) + len(service.executingClaims)
	service.mu.Unlock()
	if active != 0 {
		t.Fatal("failed preparation retained admitted/executing ownership", active)
	}

	// The same service and operation can be corrected explicitly, with no retry
	// inside PrepareCreate and no durable write or execution at either attempt.
	mutate = false
	postedAt = commandTime()
	next, err := service.PrepareCreate(context.Background(), request)
	if err != nil || next == nil || next.closed || next.owner != service || next.request.Collection.Article.PostedAt == nil || !next.request.Collection.Article.PostedAt.Equal(commandTime()) {
		t.Fatal("failed preparation prevented corrected same-operation preparation", next, err)
	}
	assertCommandCalls(t, ports, map[string]int{"operation": 2, "find": 2})
	if effects.ids.Load()+effects.entropy.calls.Load()+effects.notices.Load() != 0 {
		t.Fatal("corrected preparation performed an admission or execution effect")
	}
	next.Close()
	next.Close()
	if !next.closed || !reflect.DeepEqual(next.request, CreateCollectionRequest{}) || next.replay != nil {
		t.Fatal("prepared Close did not clear the owned snapshot idempotently")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal("failed preparation retained registered work", err)
	}
	select {
	case <-service.shutdownDone:
	default:
		t.Fatal("successful Close returned before work cleanup")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal("duplicate service Close", err)
	}
	assertCommandCalls(t, ports, map[string]int{"operation": 2, "find": 2})
}
