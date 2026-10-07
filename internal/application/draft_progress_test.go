package application

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestLoadTwentyPageProgressSequenceAndTerminalReplayAreDetachedAndMonotonic(t *testing.T) {
	observed, advance := make(chan struct{}), make(chan struct{})
	var progressCallback func(CollectionProgress)
	service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
		progressCallback = progress
		for page := uint32(1); page <= 20; page++ {
			progress(CollectionProgress{Pages: page, Comments: 2})
			select {
			case observed <- struct{}{}:
			case <-ctx.Done():
				return CollectionSnapshot{}, ctx.Err()
			}
			select {
			case <-advance:
			case <-ctx.Done():
				return CollectionSnapshot{}, ctx.Err()
			}
		}
		snapshot := snapshotFixture()
		snapshot.Pages = 20
		return snapshot, nil
	}))
	t.Cleanup(func() { service.Close() })
	request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "progress-load"), URL: "example"}
	initial, err := service.Load(context.Background(), request)
	if err != nil || initial.Load.Sequence != 0 || initial.Load.Pages != 0 || initial.Load.State != "loading" {
		t.Fatal("admission fabricated progress", initial, err)
	}
	for page := uint32(1); page <= 20; page++ {
		<-observed
		state := getDraft(t, service)
		if state.Load.Sequence != uint64(page) || state.Load.Pages != page || state.Load.Comments != 2 || state.Load.State != "loading" || state.Load.OperationID != request.OperationID || state.Summary.Revision != initial.Summary.Revision || state.Summary.ArticleGeneration != 0 || state.Summary.Snapshot != nil {
			t.Fatalf("page%d changed progress/state invariant: %+v", page, state)
		}
		state.Load.Sequence = ^uint64(0)
		state.Load.State = "completed"
		reread := getDraft(t, service)
		if reread.Load.Sequence != uint64(page) || reread.Load.State != "loading" {
			t.Fatal("progress response aliased owner")
		}
		op, queryErr := service.Operation(context.Background(), request.OperationID)
		if queryErr != nil || op.State != contracts.OperationPending {
			t.Fatal("progress query became terminal", op, queryErr)
		}
		advance <- struct{}{}
	}
	service.workers.Wait()
	terminal := getDraft(t, service)
	if terminal.Load.Sequence != 21 || terminal.Load.Pages != 20 || terminal.Load.State != "completed" || terminal.Summary.Revision != initial.Summary.Revision+1 || terminal.Summary.ArticleGeneration != 1 || terminal.Summary.Snapshot.Pages != 20 {
		t.Fatal("terminal sequence/revision changed", terminal)
	}
	replay, err := service.Load(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replay, terminal) {
		t.Fatal("terminal replay changed state", replay, err)
	}
	progressCallback(CollectionProgress{Pages: 1, Comments: 1})
	if !reflect.DeepEqual(getDraft(t, service), terminal) {
		t.Fatal("late progress changed committed terminal")
	}
}

func TestTerminalAndClosedLoadsRejectEveryLateProgressCallback(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			entered, release := make(chan func(CollectionProgress), 1), make(chan struct{})
			var once sync.Once
			service := completedDraft(t)
			service.options.Collector = testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
				entered <- progress
				select {
				case <-release:
				case <-ctx.Done():
					return CollectionSnapshot{}, ctx.Err()
				}
				if state == "failed" {
					return CollectionSnapshot{}, errors.New("collector failure")
				}
				return snapshotFixture(), nil
			})
			t.Cleanup(func() { once.Do(func() { close(release) }); service.Close() })
			if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "second-load"), URL: "example"}); err != nil {
				t.Fatal(err)
			}
			callback := <-entered
			callback(CollectionProgress{Pages: 1, Comments: 2})
			if state == "cancelled" {
				cancelled, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "cancel"), Kind: "CancelLoad"})
				if err != nil || cancelled.Load.State != "cancelling" {
					t.Fatal("cancellation claimed terminal before worker", cancelled, err)
				}
			}
			once.Do(func() { close(release) })
			service.workers.Wait()
			terminal := getDraft(t, service)
			if terminal.Load.State != state || terminal.Load.Sequence != 2 {
				t.Fatal("unexpected terminal", terminal)
			}
			opBefore, err := service.Operation(context.Background(), "second-load")
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			for i := 0; i < 32; i++ {
				workers.Add(1)
				go func() { defer workers.Done(); callback(CollectionProgress{Pages: 20, Comments: 100000}) }()
			}
			workers.Wait()
			if !reflect.DeepEqual(getDraft(t, service), terminal) {
				t.Fatal("concurrent late callbacks changed terminal")
			}
			opAfter, err := service.Operation(context.Background(), "second-load")
			if err != nil || !reflect.DeepEqual(opBefore, opAfter) {
				t.Fatal("late progress changed receipt", opAfter, err)
			}
			summary := service.Summary()
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			callback(CollectionProgress{Pages: 19, Comments: 99999})
			if !reflect.DeepEqual(service.Summary(), summary) {
				t.Fatal("closed worker callback mutated owner")
			}
		})
	}
}
