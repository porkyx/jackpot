package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestRoundHistoryEmptyFiftyFiftyOneAndEqualTimestampPagesKeepDistinctCollections(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	ctx := context.Background()
	empty, total, err := store.ListCollectionPage(ctx, 0, 50, "", "all")
	if err != nil || total != 0 || len(empty) != 0 {
		t.Fatal(empty, total, err)
	}
	ids := make([]contracts.CollectionID, 0, 51)
	rounds := make(map[contracts.CollectionID]contracts.RoundID)
	for index := 0; index < 51; index++ {
		if index == 50 {
			now = now.Add(time.Second)
		}
		request := productRequest(now, rl.ReservationMode)
		request.Context.DraftID = contracts.DraftID(fmt.Sprintf("page-draft-%02d", index))
		request.Collection.SourceDraftID = request.Context.DraftID
		request.OperationID = contracts.OperationID(fmt.Sprintf("page-op-%02d", index))
		request.Collection.Article.Title = fmt.Sprintf("한글 제목 %02d", index)
		request.Collection.Article.GalleryName = "동일 갤러리"
		result, err := service.CreateCollection(ctx, request)
		if err != nil || result.State != contracts.PendingSchedule {
			t.Fatal(result, err)
		}
		ids = append(ids, result.CollectionID)
		rounds[result.CollectionID] = result.ID
		if index == 49 {
			exact, count, err := store.ListCollectionPage(ctx, 0, 50, "", "all")
			if err != nil || count != 50 || len(exact) != 50 {
				t.Fatal("exact fifty", count, len(exact), err)
			}
			tail, count, err := store.ListCollectionPage(ctx, 50, 50, "", "all")
			if err != nil || count != 50 || len(tail) != 0 {
				t.Fatal("exact fifty tail", count, len(tail), err)
			}
		}
	}
	// The independent expected order comes from captured IDs and the explicit
	// timestamp fixture, never from another database listing or sorting helper.
	sameTime := append([]contracts.CollectionID(nil), ids[:50]...)
	sort.Slice(sameTime, func(i, j int) bool { return sameTime[i] > sameTime[j] })
	expected := append([]contracts.CollectionID{ids[50]}, sameTime...)
	var observed []contracts.CollectionID
	for _, offset := range []uint32{0, 50} {
		page, count, err := store.ListCollectionPage(ctx, offset, 50, "", "all")
		expectedLength := 50
		if offset == 50 {
			expectedLength = 1
		}
		if err != nil || count != 51 || len(page) != expectedLength {
			t.Fatal(offset, count, len(page), err)
		}
		replay, replayCount, err := store.ListCollectionPage(ctx, offset, 50, "", "all")
		if err != nil || replayCount != count || !reflect.DeepEqual(replay, page) {
			t.Fatal("unstable tie page", err)
		}
		for _, record := range page {
			if record.LatestRound == nil || record.LatestRound.ID != rounds[record.ID] || record.Revision != 1 || record.LatestRound.Revision != 1 || record.URL != productRequest(now, rl.ReservationMode).Collection.Article.URL {
				t.Fatal("collection mixed with same article peer", record)
			}
			observed = append(observed, record.ID)
		}
	}
	if !reflect.DeepEqual(observed, expected) {
		t.Fatalf("tie order/gap/duplicate: got %v want %v", observed, expected)
	}
	last, count, err := store.ListCollectionPage(ctx, 0, 50, "제목 50", "pending_schedule")
	if err != nil || count != 1 || len(last) != 1 || last[0].ID != ids[50] {
		t.Fatal("global search omitted next-page collection", last, count, err)
	}
	if entropy.calls.Load() != 0 || roundTableCount(t, store, "collections") != 51 || roundTableCount(t, store, "results") != 0 {
		t.Fatal("readonly listing executed or changed collection", entropy.calls.Load())
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}

func TestRoundUserAndScheduledPublicationFailureNeverExposesUncommittedOutcomeOrRepeatsEntropy(t *testing.T) {
	for _, entry := range []string{"user", "scheduler"} {
		t.Run(entry, func(t *testing.T) {
			store := migratedTestStore(t)
			now := storageNow
			entropy := &productEntropy{entered: make(chan struct{}, 1), release: make(chan struct{})}
			var release sync.Once
			releaseEntropy := func() { release.Do(func() { close(entropy.release) }) }
			t.Cleanup(releaseEntropy)
			type observation struct {
				notice contracts.StateNotice
				round  rl.RoundRecord
			}
			var mu sync.Mutex
			var notices []observation
			service := productService(t, store, entropy, func() time.Time { return now }, "session-one", func(ctx context.Context, notice contracts.StateNotice) error {
				_, rounds, revision, err := store.ReadCollectionState(ctx, contracts.CollectionID(notice.EntityID))
				if err != nil || len(rounds) != 1 || revision != notice.Revision {
					t.Errorf("notification precedes commit: rounds=%d revision=%d notice=%+v err=%v", len(rounds), revision, notice, err)
				} else {
					mu.Lock()
					notices = append(notices, observation{notice, rounds[0]})
					mu.Unlock()
				}
				return errors.New("continuous notification failure")
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request := productRequest(now, rl.ImmediateMode)
			var scheduled rl.RoundRecord
			if entry == "scheduler" {
				request.Input.Mode = rl.ReservationMode
				pending, err := service.CreateCollection(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				seconds := uint32(30)
				scheduled, err = service.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: "publication-schedule", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &seconds, Timezone: "Asia/Seoul"})
				if err != nil {
					t.Fatal(err)
				}
				now = *scheduled.ScheduledAt
				mu.Lock()
				notices = nil
				mu.Unlock()
			}
			finished := make(chan roundCommandResult, 1)
			workerJoined := false
			t.Cleanup(func() {
				releaseEntropy()
				if !workerJoined {
					cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cleanupCancel()
					select {
					case <-finished:
					case <-cleanupContext.Done():
						t.Error("publication worker cleanup", cleanupContext.Err())
					}
				}
			})
			go func() {
				var result rl.RoundRecord
				var err error
				if entry == "user" {
					result, err = service.CreateCollection(ctx, request)
				} else {
					result, err = service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: scheduled.ID})
				}
				finished <- roundCommandResult{round: result, err: err}
			}()
			awaitShutdownBarrier(t, ctx, entropy.entered)
			mu.Lock()
			before := append([]observation(nil), notices...)
			mu.Unlock()
			if len(before) != 1 || before[0].round.State != contracts.Executing || before[0].round.Outcome != nil || before[0].notice.OperationID == nil || roundTableCount(t, store, "results") != 0 || roundTableCount(t, store, "winners") != 0 {
				t.Fatal("pre-commit winner exposed", before)
			}
			select {
			case early := <-finished:
				workerJoined = true
				t.Fatal("returned while entropy is blocked", early)
			default:
			}
			releaseEntropy()
			result := receiveShutdown(t, ctx, finished)
			workerJoined = true
			if result.err != nil || result.round.State != contracts.Completed || result.round.Outcome == nil || len(result.round.Outcome.Winners) != 1 || result.round.Outcome.Winners[0].ParticipantID != "p1" {
				t.Fatal(result)
			}
			_, storedRounds, revision, err := store.ReadCollectionState(ctx, result.round.CollectionID)
			if err != nil || len(storedRounds) != 1 || revision != result.round.Revision || !reflect.DeepEqual(storedRounds[0], result.round) {
				t.Fatal("read differs after failed notification", storedRounds, revision, err)
			}
			mu.Lock()
			after := append([]observation(nil), notices...)
			mu.Unlock()
			if len(after) != 2 || after[1].round.State != contracts.Completed || !reflect.DeepEqual(after[1].round.Outcome, result.round.Outcome) {
				t.Fatal("successful outcome notice missing committed result", after)
			}
			var replay rl.RoundRecord
			if entry == "user" {
				replay, err = service.CreateCollection(ctx, request)
			} else {
				replay, err = service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: scheduled.ID})
			}
			if err != nil || !reflect.DeepEqual(replay, result.round) || entropy.calls.Load() != 1 || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
				t.Fatal("notification retry executed again", replay, err, entropy.calls.Load())
			}
			mu.Lock()
			count := len(notices)
			mu.Unlock()
			if count != 2 {
				t.Fatal("replay re-published notification", count)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
