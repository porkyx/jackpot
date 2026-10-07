package desktop

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestCollectionPageActualSQLiteFiftyBoundaryAnchorAndLatestManagement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "page-session", clock, false)
	boundary, lifecycle := roundBoundary(t, store, draft, "page-session", clock, new(roundBoundaryEntropy))
	frozen := rl.FrozenCollection{SourceDraftID: "page-draft", FinalizedDraftRevision: 1, ArticleGeneration: 1, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "page fixture"}, Snapshot: contracts.SnapshotSummary{SnapshotID: "page-snapshot", CollectedAt: now, Complete: true, Pages: 1, AcceptedComments: 2}}
	for _, id := range []contracts.ParticipantID{"first", "second"} {
		frozen.Participants = append(frozen.Participants, rl.ParticipantSnapshot{ID: id, Nickname: string(id), PublicIdentifier: string(id), Kind: "fixed", Included: true, Classification: "included", Comments: []rl.CommentSnapshot{{ID: string(id) + "-comment", Kind: "text", Text: "synthetic", PostedAt: &now}}})
	}
	record, err := lifecycle.CreateCollection(ctx, rl.CreateCollectionRequest{OperationID: "page-create", Context: contracts.DraftContext{BackendSessionID: "page-session", DraftID: "page-draft", Revision: 1, ArticleGeneration: 1}, Collection: frozen, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"first", "second"}, Prizes: []rl.Prize{{ID: "prize", Name: "prize", Count: 1}}, Mode: rl.ReservationMode}})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := boundary.GetCollection(ctx, contracts.CollectionQuery{CollectionID: record.CollectionID})
	data := requireCollectionOK(t, reply, err)
	firstID := data.LatestRound.RoundID
	for number := 1; number <= 51; number++ {
		response, err := boundary.CancelSchedule(ctx, roundCommand(data, "page-session", fmt.Sprintf("page-cancel-%d", number)))
		data = requireCollectionOK(t, response, err)
		if number == 51 {
			break
		}
		request := roundCommand(data, "page-session", fmt.Sprintf("page-rerun-%d", number))
		request.Mode = rl.ReservationMode
		request.Prizes = []contracts.PrizeInput{{ID: "prize", Name: "prize", Count: 1}}
		response, err = boundary.Rerun(ctx, request)
		data = requireCollectionOK(t, response, err)
	}
	before, allRounds, revision, err := store.ReadCollectionState(ctx, record.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if data.RoundTotal != 51 || data.RoundOffset != 0 || len(data.Rounds) != 50 || data.Rounds[0].Number != 2 || data.Rounds[49].Number != 51 || data.LatestRound.Number != 51 || data.RemainingCount != 2 {
		t.Fatal("latest bounded page", data.RoundTotal, data.RoundOffset, len(data.Rounds))
	}
	latestID := data.LatestRound.RoundID
	for _, item := range []struct {
		name        string
		query       contracts.CollectionQuery
		offset      uint32
		first, last uint32
		count       int
	}{
		{"default", contracts.CollectionQuery{CollectionID: record.CollectionID}, 0, 2, 51, 50},
		{"explicit-fifty", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundLimit: 50}, 0, 2, 51, 50},
		{"one-latest", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundLimit: 1}, 0, 51, 51, 1},
		{"older-first", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundOffset: 50, RoundLimit: 50}, 50, 1, 1, 1},
		{"first-anchor", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundID: firstID}, 50, 1, 1, 1},
		{"latest-anchor", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundID: latestID}, 0, 2, 51, 50},
		{"past-end", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundOffset: 51, RoundLimit: 50}, 51, 0, 0, 0},
		{"maximum-offset", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundOffset: math.MaxUint32, RoundLimit: 50}, math.MaxUint32, 0, 0, 0},
	} {
		t.Run(item.name, func(t *testing.T) {
			response, err := boundary.GetCollection(ctx, item.query)
			page := requireCollectionOK(t, response, err)
			if len(page.Rounds) != item.count || page.RoundOffset != item.offset || page.RoundTotal != 51 || page.LatestRound.RoundID != latestID || page.LatestRound.Number != 51 || page.Revision != revision || page.RemainingCount != 2 {
				t.Fatal("page metadata/management reference", page.RoundOffset, len(page.Rounds))
			}
			if item.count > 0 && (page.Rounds[0].Number != item.first || page.Rounds[item.count-1].Number != item.last) {
				t.Fatal("page order")
			}
		})
	}
	for _, item := range []struct {
		name  string
		query contracts.CollectionQuery
	}{
		{"limit-fifty-one", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundLimit: 51}},
		{"maximum-limit", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundLimit: math.MaxUint32}},
		{"anchor-and-offset", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundID: firstID, RoundOffset: 1, RoundLimit: 50}},
		{"missing-anchor", contracts.CollectionQuery{CollectionID: record.CollectionID, RoundID: "missing"}},
		{"missing-collection", contracts.CollectionQuery{CollectionID: "missing"}},
		{"empty-collection", contracts.CollectionQuery{}},
	} {
		t.Run(item.name, func(t *testing.T) {
			response, err := boundary.GetCollection(ctx, item.query)
			if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidInput {
				t.Fatal("invalid page accepted", err, response.Code)
			}
		})
	}
	after, afterRounds, afterRevision, err := store.ReadCollectionState(ctx, record.CollectionID)
	if err != nil || revision != afterRevision || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(allRounds, afterRounds) {
		t.Fatal("query/failure mutated durable state", err)
	}
}
