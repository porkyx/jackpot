package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestCheckedQueryExactEnvelopeSizePreservesCompleteDataAndTypedFailure(t *testing.T) {
	header, err := contracts.NewResponseHeader("query-size", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	revision := contracts.Revision(7)
	operation := contracts.OperationID("read-reference")
	text := ""
	original := contracts.Envelope[string]{ResponseHeader: header, OK: true, Data: &text, Revision: &revision, OperationID: &operation}
	empty, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			body := strings.Repeat("x", contracts.MaxQueryResponseBytes-len(empty)+delta)
			response := original
			response.Data = &body
			wire, err := json.Marshal(response)
			if err != nil || len(wire) != contracts.MaxQueryResponseBytes+delta {
				t.Fatal("wire oracle", len(wire), err)
			}
			before := response
			checked, err := checkedQuery(response)
			if err != nil {
				t.Fatal(err)
			}
			if delta > 0 {
				if checked.OK || checked.Data != nil || checked.Code != contracts.ProtocolError || checked.MessageKey != string(contracts.ProtocolError) {
					t.Fatal("explicit size failure", checked)
				}
				if checked.ResponseHeader != header || checked.Revision == nil || checked.OperationID == nil || *checked.Revision != revision || *checked.OperationID != operation {
					t.Fatal("query identity lost")
				}
			} else if !reflect.DeepEqual(checked, response) {
				t.Fatal("accepted page was truncated or changed")
			}
			if !reflect.DeepEqual(before, response) || len(body) != len(wire)-len(empty) {
				t.Fatal("read size guard changed original")
			}
		})
	}
}
func TestCheckedQueryInvalidShapeKeepsValidationError(t *testing.T) {
	original := contracts.Envelope[string]{OK: true}
	checked, err := checkedQuery(original)
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != contracts.ProtocolError || !reflect.DeepEqual(checked, original) {
		t.Fatal(checked, err)
	}
}
func TestCheckedQueryUnencodableDataReturnsTypedFailure(t *testing.T) {
	header, _ := contracts.NewResponseHeader("query-size", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	value := math.NaN()
	checked, err := checkedQuery(contracts.Envelope[float64]{ResponseHeader: header, OK: true, Data: &value})
	if err != nil || checked.OK || checked.Data != nil || checked.Code != contracts.ProtocolError || checked.ResponseHeader != header {
		t.Fatal(checked, err)
	}
}
func TestDraftMutationOutcomeIsNotRewrittenByReadSizeLimit(t *testing.T) {
	header, _ := contracts.NewResponseHeader("query-size", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	operation := contracts.OperationID("durable-operation")
	data := contracts.DraftData{Summary: contracts.DraftSummary{DraftContext: contracts.DraftContext{BackendSessionID: "query-size", DraftID: "draft", Revision: 1}}}
	data.Article = &contracts.ArticleData{Title: strings.Repeat("x", contracts.MaxQueryResponseBytes)}
	service := new(DraftService)
	mutation, err := service.respond(header, data, &operation, nil)
	if err != nil || !mutation.OK || mutation.OperationID == nil || *mutation.OperationID != operation || mutation.Data.Article.Title != data.Article.Title {
		t.Fatal("mutation outcome changed", err)
	}
	query, err := service.respond(header, data, nil, nil)
	if err != nil || query.OK || query.Data != nil || query.Code != contracts.ProtocolError {
		t.Fatal("query cap bypassed", err)
	}
}
func TestFrozenCommentsLargeEscapedPageFailsWithoutTruncatingActualSQLiteSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "size-session", clock, false)
	boundary, lifecycle := roundBoundary(t, store, draft, "size-session", clock, new(roundBoundaryEntropy))
	frozen := rl.FrozenCollection{SourceDraftID: "large-draft", FinalizedDraftRevision: 1, ArticleGeneration: 1, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "size fixture"}, Snapshot: contracts.SnapshotSummary{SnapshotID: "large-snapshot", CollectedAt: now, Complete: true, Pages: 1, AcceptedComments: 51}}
	participant := rl.ParticipantSnapshot{ID: "participant", Nickname: "synthetic", PublicIdentifier: "public", Kind: "fixed", Included: true, Classification: "included"}
	for i := 0; i < 50; i++ {
		participant.Comments = append(participant.Comments, rl.CommentSnapshot{ID: fmt.Sprintf("comment-%d", i), Kind: "text", Text: strings.Repeat(">", 64*1024), PostedAt: &now})
	}
	second := rl.ParticipantSnapshot{ID: "second", Nickname: "second", PublicIdentifier: "second-public", Kind: "fixed", Included: true, Classification: "included", Comments: []rl.CommentSnapshot{{ID: "second-comment", Kind: "text", Text: "small", PostedAt: &now}}}
	frozen.Participants = []rl.ParticipantSnapshot{participant, second}
	record, err := lifecycle.CreateCollection(ctx, rl.CreateCollectionRequest{OperationID: "large-create", Context: contracts.DraftContext{BackendSessionID: "size-session", DraftID: "large-draft", Revision: 1, ArticleGeneration: 1}, Collection: frozen, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"participant", "second"}, Prizes: []rl.Prize{{ID: "prize", Name: "prize", Count: 1}}, Mode: rl.ReservationMode}})
	if err != nil {
		t.Fatal(err)
	}
	before, rounds, revision, err := store.ReadCollectionState(ctx, record.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := boundary.QueryFrozenComments(ctx, contracts.FrozenCommentsQuery{CollectionID: record.CollectionID, ParticipantID: "participant", Limit: 50})
	if err != nil || response.OK || response.Code != contracts.ProtocolError || response.Data != nil || response.MessageKey != string(contracts.ProtocolError) {
		t.Fatal("oversized page was silently accepted/truncated", err, response.Code)
	}
	for _, entry := range []struct{ offset, limit, want uint32 }{{0, 10, 10}, {49, 1, 1}, {50, 50, 0}} {
		page, err := boundary.QueryFrozenComments(ctx, contracts.FrozenCommentsQuery{CollectionID: record.CollectionID, ParticipantID: "participant", Offset: entry.offset, Limit: entry.limit})
		if err != nil || !page.OK || page.Data.Total != 50 || len(page.Data.Rows) != int(entry.want) {
			t.Fatal("complete bounded query failed", err)
		}
		for _, row := range page.Data.Rows {
			if row.Text != participant.Comments[0].Text {
				t.Fatal("stored comment was truncated")
			}
		}
	}
	after, afterRounds, afterRevision, err := store.ReadCollectionState(ctx, record.CollectionID)
	if err != nil || revision != afterRevision || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(rounds, afterRounds) {
		t.Fatal("read failure changed durable state or retained a connection", err)
	}
}
