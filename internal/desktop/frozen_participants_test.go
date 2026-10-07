package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/porkyx/jackpot/internal/application"
	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

func frozenBoundaryFixture(t *testing.T) (*RoundService, *app.DraftService, contracts.CollectionData) {
	t.Helper()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := roundStore(t)
	draft := roundDraft(t, "session-one", clock, true)
	configuration, err := draft.Get(context.Background(), contracts.DraftQuery{BackendSessionID: "session-one", DraftID: draft.Summary().DraftID})
	if err != nil {
		t.Fatal(err)
	}
	filters := configuration.Filters
	filters.ExcludeAnonymous = true
	filters.IncludeKeywords = []string{"one"}
	if _, err = draft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: roundDraftHeader(draft, "classify"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	boundary, _ := roundBoundary(t, store, draft, "session-one", clock, new(roundBoundaryEntropy))
	response, err := boundary.CreateCollection(context.Background(), contracts.CreateCollectionRequest{DraftMutationHeader: roundDraftHeader(draft, "freeze"), Mode: rl.ImmediateMode})
	return boundary, draft, requireCollectionOK(t, response, err)
}
func TestFrozenParticipantsRealSQLitePagesSearchGroupsAndPreservesOrder(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	query := contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 1}
	first, err := boundary.QueryFrozenParticipants(context.Background(), query)
	if err != nil || !first.OK || first.Data.Total != 3 || first.Data.Matched != 3 || len(first.Data.Rows) != 1 || first.Data.Rows[0].Nickname != "가" || first.Data.Rows[0].Classification != "included" || first.Data.Rows[0].CommentCount != 1 || len(first.Data.Rows[0].Previews) != 1 || first.Data.Rows[0].Previews[0] != "one" || first.Validate() != nil {
		t.Fatal(first, err)
	}
	query.Offset = 1
	second, err := boundary.QueryFrozenParticipants(context.Background(), query)
	if err != nil || !second.OK || len(second.Data.Rows) != 1 || second.Data.Rows[0].Nickname != "나" || second.Data.Offset != 1 {
		t.Fatal(second, err)
	}
	query.Offset = 0
	query.Query = "USER1"
	query.Limit = 100
	search, err := boundary.QueryFrozenParticipants(context.Background(), query)
	if err != nil || !search.OK || search.Data.Total != 3 || search.Data.Matched != 1 || search.Data.Rows[0].ID != first.Data.Rows[0].ID {
		t.Fatal(search, err)
	}
	query.Query = ""
	for _, entry := range []struct{ group, nickname string }{{"included", "가"}, {"excluded", "나"}, {"unclassified", "다"}} {
		query.Group = entry.group
		page, err := boundary.QueryFrozenParticipants(context.Background(), query)
		if err != nil || !page.OK || page.Data.Matched != 1 || page.Data.Rows[0].Nickname != entry.nickname {
			t.Fatal(page, err)
		}
	}
	query.Group = ""
	query.Offset = math.MaxUint32
	empty, err := boundary.QueryFrozenParticipants(context.Background(), query)
	if err != nil || !empty.OK || empty.Data.Matched != 3 || empty.Data.Rows == nil || len(empty.Data.Rows) != 0 {
		t.Fatal(empty, err)
	}
	first.Data.Rows[0].Nickname = "mutation"
	query.Offset = 0
	again, err := boundary.QueryFrozenParticipants(context.Background(), query)
	if err != nil || again.Data.Rows[0].Nickname != "가" {
		t.Fatal(again, err)
	}
	raw, _ := json.Marshal(again)
	for _, private := range []string{"password-one", `"Credential"`, `"Comments"`, `"Salt"`, `"Key"`} {
		if strings.Contains(string(raw), private) {
			t.Fatal("unbounded/private snapshot in participant page", private)
		}
	}
}
func TestFrozenCommentsRealSQLitePagesAreDetachedAndReadSurvivesNewSession(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	participants, err := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 100})
	if err != nil || !participants.OK {
		t.Fatal(participants, err)
	}
	pid := participants.Data.Rows[0].ID
	expected := collection.Revision
	query := contracts.FrozenCommentsQuery{CollectionID: collection.CollectionID, ExpectedRevision: &expected, ParticipantID: pid, Limit: 50}
	comments, err := boundary.QueryFrozenComments(context.Background(), query)
	if err != nil || !comments.OK || comments.Data.Total != 1 || comments.Data.Rows[0].Text != "one" || comments.Data.Revision != expected || comments.Validate() != nil {
		t.Fatal(comments, err)
	}
	*comments.Data.Rows[0].PostedAt = comments.Data.Rows[0].PostedAt.Add(time.Hour)
	comments.Data.Rows[0].Text = "mutation"
	comments.Data.Rows[0].MediaURLs = append(comments.Data.Rows[0].MediaURLs, "unsafe")
	again, err := boundary.QueryFrozenComments(context.Background(), query)
	if err != nil || again.Data.Rows[0].Text != "one" || len(again.Data.Rows[0].MediaURLs) != 0 || again.Data.Rows[0].PostedAt.Hour() != 0 {
		t.Fatal(again, err)
	}
	query.Offset = math.MaxUint32
	empty, err := boundary.QueryFrozenComments(context.Background(), query)
	if err != nil || !empty.OK || empty.Data.Total != 1 || empty.Data.Rows == nil || len(empty.Data.Rows) != 0 {
		t.Fatal(empty, err)
	}
	// The new current draft has its own mutable filters; historical pages use only
	// the saved collection and do not require an unlock grant in this session.
	freshDraft := roundDraft(t, "session-two", boundary.now, true)
	configuration, err := freshDraft.Get(context.Background(), contracts.DraftQuery{BackendSessionID: "session-two", DraftID: freshDraft.Summary().DraftID})
	if err != nil {
		t.Fatal(err)
	}
	filters := configuration.Filters
	filters.ExcludeAnonymous = false
	filters.IncludeKeywords = []string{"changed"}
	if _, err = freshDraft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: roundDraftHeader(freshDraft, "change-current"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewRoundService(freshDraft, boundary.lifecycle, boundary.reader, "session-two", boundary.now)
	if err != nil {
		t.Fatal(err)
	}
	page, err := fresh.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 100})
	if err != nil || !page.OK || page.BackendSessionID != "session-two" || page.Data.Rows[1].Classification != "excluded" || page.Data.Rows[0].Classification != "included" {
		t.Fatal(page, err)
	}
}
func TestFrozenQueriesRejectBadBoundsIDsGroupsCountersAndStaleRevision(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	for _, entry := range []contracts.FrozenParticipantsQuery{{Limit: 100}, {CollectionID: collection.CollectionID}, {CollectionID: collection.CollectionID, Limit: 101}, {CollectionID: collection.CollectionID, Limit: 1, Group: "manual"}, {CollectionID: collection.CollectionID, Limit: 1, Query: string([]byte{255})}, {CollectionID: collection.CollectionID, Limit: 1, Query: strings.Repeat("x", 1001)}, {CollectionID: "missing", Limit: 1}} {
		response, err := boundary.QueryFrozenParticipants(context.Background(), entry)
		if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidInput || response.Validate() != nil {
			t.Fatal(response, err)
		}
	}
	unsafe := contracts.Revision(contracts.MaxSafeInteger + 1)
	for _, revision := range []*contracts.Revision{&unsafe, new(contracts.Revision)} {
		response, err := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, ExpectedRevision: revision, Limit: 1})
		code := contracts.StaleRevision
		if *revision > contracts.Revision(contracts.MaxSafeInteger) {
			code = contracts.InvalidInput
		}
		if err != nil || response.OK || response.Code != code || response.Data != nil {
			t.Fatal(response, err)
		}
	}
	participants, _ := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 100})
	pid := participants.Data.Rows[0].ID
	for _, entry := range []contracts.FrozenCommentsQuery{{ParticipantID: pid, Limit: 50}, {CollectionID: collection.CollectionID, Limit: 1}, {CollectionID: collection.CollectionID, ParticipantID: pid}, {CollectionID: collection.CollectionID, ParticipantID: pid, Limit: 51}, {CollectionID: collection.CollectionID, ParticipantID: "missing", Limit: 1}, {CollectionID: collection.CollectionID, ParticipantID: pid, ExpectedRevision: &unsafe, Limit: 1}} {
		response, err := boundary.QueryFrozenComments(context.Background(), entry)
		if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidInput || response.Validate() != nil {
			t.Fatal(response, err)
		}
	}
	zero := contracts.Revision(0)
	stale, err := boundary.QueryFrozenComments(context.Background(), contracts.FrozenCommentsQuery{CollectionID: collection.CollectionID, ParticipantID: pid, ExpectedRevision: &zero, Limit: 1})
	if err != nil || stale.OK || stale.Code != contracts.StaleRevision {
		t.Fatal(stale, err)
	}
	if _, err = boundary.QueryFrozenParticipants(nil, contracts.FrozenParticipantsQuery{}); err == nil {
		t.Fatal("nil context")
	}
	if _, err = boundary.QueryFrozenComments(nil, contracts.FrozenCommentsQuery{}); err == nil {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = boundary.QueryFrozenParticipants(ctx, contracts.FrozenParticipantsQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = boundary.QueryFrozenComments(ctx, contracts.FrozenCommentsQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestFrozenProjectionsKeepUntrustedTextPlainClonePointersAndRejectUnsafeMedia(t *testing.T) {
	participant := rl.ParticipantSnapshot{ID: "p", Kind: "anonymous", Nickname: "<script>", PublicIdentifier: "ip", Classification: "unclassified", Comments: []rl.CommentSnapshot{{Text: strings.Repeat("😀", 141)}}}
	data, err := frozenParticipantProjection(participant)
	if err != nil || data.Nickname != "<script>" || len(data.Previews) != 1 || data.Previews[0] != strings.Repeat("😀", 127)+"…" {
		t.Fatal(data, err)
	}
	stamp := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	parent := "parent"
	originalStamp := stamp
	comment := rl.CommentSnapshot{ID: "c", ParentID: &parent, Kind: "text", Text: "<img src=x onerror=alert(1)>", PostedAt: &stamp, MediaURLs: []string{"https://dcimg5.dcinside.com/a.png"}}
	projection, err := frozenCommentProjection(comment)
	if err != nil || projection.Text != comment.Text {
		t.Fatal(projection, err)
	}
	*projection.ParentID = "mutation"
	*projection.PostedAt = originalStamp.Add(time.Hour)
	projection.MediaURLs[0] = "mutation"
	if *comment.ParentID != "parent" || !comment.PostedAt.Equal(originalStamp) || comment.MediaURLs[0] != "https://dcimg5.dcinside.com/a.png" {
		t.Fatal("projection alias")
	}
	for _, raw := range []string{"javascript:alert(1)", "http://dcimg5.dcinside.com/a", "https://user@dcimg5.dcinside.com/a", "https://dcimg5.dcinside.com:443/a", "https://dcimg5.dcinside.com.evil/a", "https://[invalid", "relative"} {
		bad := comment
		bad.MediaURLs = []string{raw}
		if _, err = frozenCommentProjection(bad); err == nil {
			t.Fatal("unsafe media", raw)
		}
	}
	for _, host := range []string{"dcimg4.dcinside.com", "dcimg1.dcinside.com", "dccon.dcinside.com", "image.dcinside.com"} {
		safe := comment
		safe.MediaURLs = []string{"https://" + host + "/a"}
		if _, err = frozenCommentProjection(safe); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []rl.CommentSnapshot{{Kind: "text"}, {ID: "c", Kind: "unknown"}, {ID: "c", Kind: "text", Text: string([]byte{255})}, {ID: "c", Kind: "text", ParentID: new(string)}, {ID: "c", Kind: "text", PostedAt: new(time.Time)}} {
		if _, err = frozenCommentProjection(bad); err == nil {
			t.Fatal("invalid stored comment")
		}
	}
	for _, bad := range []rl.ParticipantSnapshot{{Classification: "unclassified"}, {ID: "p", Classification: "unknown"}, {ID: "p", Classification: "included", Nickname: string([]byte{255})}, {ID: "p", Classification: "excluded", PublicIdentifier: string([]byte{255})}, {ID: "p", Classification: "unclassified", Comments: []rl.CommentSnapshot{{Text: string([]byte{255})}}}} {
		if _, err = frozenParticipantProjection(bad); err == nil {
			t.Fatal("invalid stored participant")
		}
	}
}

type frozenReadFault struct {
	ProductReader
	calls      atomic.Int32
	nth        int32
	continuous bool
}

func (reader *frozenReadFault) ReadCollectionState(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error) {
	call := reader.calls.Add(1)
	if reader.continuous || call == reader.nth {
		return rl.FrozenCollection{Participants: []rl.ParticipantSnapshot{{ID: "partial-private"}}}, nil, 1, errors.New("private storage fault")
	}
	return reader.ProductReader.ReadCollectionState(ctx, id)
}
func TestFrozenPageReadFailuresNeverPublishPartialDataOrMutateSavedSnapshot(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	original, rounds, revision, err := boundary.reader.ReadCollectionState(context.Background(), collection.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		nth        int32
		continuous bool
	}{{1, false}, {2, false}, {0, true}} {
		fault := &frozenReadFault{ProductReader: boundary.reader, nth: entry.nth, continuous: entry.continuous}
		copy := *boundary
		copy.reader = fault
		for call := int32(1); call <= 3; call++ {
			page, err := copy.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			failed := entry.continuous || call == entry.nth
			if failed {
				if page.OK || page.Data != nil || page.Code != contracts.StorageUnavailable {
					t.Fatal(page)
				}
			} else if !page.OK || len(page.Data.Rows) != 1 {
				t.Fatal(page)
			}
		}
	}
	again, currentRounds, currentRevision, err := boundary.reader.ReadCollectionState(context.Background(), collection.CollectionID)
	if err != nil || !reflect.DeepEqual(original, again) || !reflect.DeepEqual(rounds, currentRounds) || revision != currentRevision {
		t.Fatal("read altered durable state", err)
	}
}

func TestFrozenRealSQLiteUpperPageBoundsAndRestartKeepStablePositions(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	path := filepath.Join(t.TempDir(), "frozen-page.sqlite3")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	draft := roundDraft(t, "session-one", clock, false)
	boundary, lifecycle := roundBoundary(t, store, draft, "session-one", clock, new(roundBoundaryEntropy))
	frozen := rl.FrozenCollection{SourceDraftID: "bounded-draft", FinalizedDraftRevision: 4, ArticleGeneration: 2, Snapshot: contracts.SnapshotSummary{SnapshotID: "bounded-snapshot", CollectedAt: now, Complete: true, Pages: 1, AcceptedComments: 299}, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "bounded"}}
	candidates := make([]contracts.ParticipantID, 0, 180)
	for i := 0; i < 180; i++ {
		id := contracts.ParticipantID(fmt.Sprintf("p-%03d", i))
		participant := rl.ParticipantSnapshot{ID: id, Nickname: fmt.Sprintf("참가자 %03d", i), PublicIdentifier: fmt.Sprintf("user-%03d", i), Kind: "fixed", Included: true, Classification: "included"}
		count := 1
		if i == 0 {
			count = 120
		}
		for j := 0; j < count; j++ {
			text := fmt.Sprintf("댓글 %03d", j)
			participant.Comments = append(participant.Comments, rl.CommentSnapshot{ID: fmt.Sprintf("c-%03d-%03d", i, j), Kind: "text", Text: text, PostedAt: &now})
		}
		frozen.Participants = append(frozen.Participants, participant)
		candidates = append(candidates, id)
	}
	record, err := lifecycle.CreateCollection(context.Background(), rl.CreateCollectionRequest{OperationID: "create-bounded", Context: contracts.DraftContext{BackendSessionID: "session-one", DraftID: "bounded-draft", Revision: 4, ArticleGeneration: 2}, Collection: frozen, Input: rl.RoundInput{CandidateIDs: candidates, Prizes: []rl.Prize{{ID: "prize", Name: "상품", Count: 1}}, Mode: rl.ReservationMode}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ offset, want uint32 }{{0, 100}, {100, 80}, {180, 0}} {
		page, err := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: record.CollectionID, Offset: entry.offset, Limit: 100})
		if err != nil || !page.OK || page.Data.Total != 180 || page.Data.Matched != 180 || uint32(len(page.Data.Rows)) != entry.want {
			t.Fatal(page, err)
		}
		for index, row := range page.Data.Rows {
			if row.ID != contracts.ParticipantID(fmt.Sprintf("p-%03d", int(entry.offset)+index)) {
				t.Fatal("participant order", row.ID)
			}
		}
	}
	for _, entry := range []struct{ offset, want uint32 }{{0, 50}, {50, 50}, {100, 20}, {120, 0}} {
		page, err := boundary.QueryFrozenComments(context.Background(), contracts.FrozenCommentsQuery{CollectionID: record.CollectionID, ParticipantID: "p-000", Offset: entry.offset, Limit: 50})
		if err != nil || !page.OK || page.Data.Total != 120 || uint32(len(page.Data.Rows)) != entry.want {
			t.Fatal(page, err)
		}
		for index, row := range page.Data.Rows {
			if row.ID != fmt.Sprintf("c-000-%03d", int(entry.offset)+index) {
				t.Fatal("comment order", row.ID)
			}
		}
	}
	if err = lifecycle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	freshDraft := roundDraft(t, "session-two", clock, false)
	fresh, _ := roundBoundary(t, reopened, freshDraft, "session-two", clock, new(roundBoundaryEntropy))
	revision := record.Revision
	page, err := fresh.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: record.CollectionID, ExpectedRevision: &revision, Query: "USER-179", Limit: 100})
	if err != nil || !page.OK || page.BackendSessionID != "session-two" || len(page.Data.Rows) != 1 || page.Data.Rows[0].ID != "p-179" {
		t.Fatal(page, err)
	}
	comments, err := fresh.QueryFrozenComments(context.Background(), contracts.FrozenCommentsQuery{CollectionID: record.CollectionID, ExpectedRevision: &revision, ParticipantID: "p-000", Offset: 119, Limit: 50})
	if err != nil || !comments.OK || len(comments.Data.Rows) != 1 || comments.Data.Rows[0].Text != "댓글 119" {
		t.Fatal(comments, err)
	}
}

type frozenReadAlter struct {
	ProductReader
	alter func(*rl.FrozenCollection)
	after func()
}

func (reader frozenReadAlter) ReadCollectionState(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error) {
	frozen, rounds, revision, err := reader.ProductReader.ReadCollectionState(ctx, id)
	if err == nil {
		if reader.alter != nil {
			reader.alter(&frozen)
		}
		if reader.after != nil {
			reader.after()
		}
	}
	return frozen, rounds, revision, err
}

type frozenCancelAtErr struct {
	context.Context
	cancel    context.CancelFunc
	remaining atomic.Int32
}

func (ctx *frozenCancelAtErr) Err() error {
	if remaining := ctx.remaining.Load(); remaining > 0 && ctx.remaining.Add(-1) == 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
func TestFrozenQueriesStorageProjectionHeaderAndMidReadCancellationFailClosed(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	page, err := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 1})
	if err != nil || !page.OK {
		t.Fatal(page, err)
	}
	pid := page.Data.Rows[0].ID
	participants := contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Limit: 1}
	comments := contracts.FrozenCommentsQuery{CollectionID: collection.CollectionID, ParticipantID: pid, Limit: 1}
	// Corrupt persisted projections never become a partly valid successful page.
	participantCorrupt := *boundary
	participantCorrupt.reader = frozenReadAlter{ProductReader: boundary.reader, alter: func(frozen *rl.FrozenCollection) { frozen.Participants[0].ID = "" }}
	response, err := participantCorrupt.QueryFrozenParticipants(context.Background(), participants)
	if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidState {
		t.Fatal(response, err)
	}
	commentCorrupt := *boundary
	commentCorrupt.reader = frozenReadAlter{ProductReader: boundary.reader, alter: func(frozen *rl.FrozenCollection) { frozen.Participants[0].Comments[0].Kind = "invalid" }}
	detail, err := commentCorrupt.QueryFrozenComments(context.Background(), comments)
	if err != nil || detail.OK || detail.Data != nil || detail.Code != contracts.InvalidState {
		t.Fatal(detail, err)
	}
	for _, nth := range []int32{1, 2, 0} {
		fault := *boundary
		fault.reader = &frozenReadFault{ProductReader: boundary.reader, nth: nth, continuous: nth == 0}
		for call := int32(1); call <= 3; call++ {
			detail, err := fault.QueryFrozenComments(context.Background(), comments)
			if err != nil {
				t.Fatal(err)
			}
			if nth == 0 || nth == call {
				if detail.OK || detail.Data != nil || detail.Code != contracts.StorageUnavailable {
					t.Fatal(detail)
				}
			} else if !detail.OK {
				t.Fatal(detail)
			}
		}
	}
	for _, phase := range []int32{1, 2} {
		// Arm only after the real SQLite read has completed. The second check is
		// inside the selected participant's comment loop; neither test sleeps.
		base, cancel := context.WithCancel(context.Background())
		ctx := &frozenCancelAtErr{Context: base, cancel: cancel}
		copy := *boundary
		copy.reader = frozenReadAlter{ProductReader: boundary.reader, after: func() { ctx.remaining.Store(phase) }}
		response, err := copy.QueryFrozenComments(ctx, comments)
		cancel()
		if err != nil || response.OK || response.Data != nil || response.Code != contracts.StorageUnavailable {
			t.Fatal(response, err)
		}
	}
	base, cancel := context.WithCancel(context.Background())
	ctx := &frozenCancelAtErr{Context: base, cancel: cancel}
	copy := *boundary
	copy.reader = frozenReadAlter{ProductReader: boundary.reader, after: func() { ctx.remaining.Store(1) }}
	response, err = copy.QueryFrozenParticipants(ctx, participants)
	cancel()
	if err != nil || response.OK || response.Data != nil || response.Code != contracts.StorageUnavailable {
		t.Fatal(response, err)
	}
	invalidHeader := *boundary
	invalidHeader.now = func() time.Time { return time.Time{} }
	if _, err = invalidHeader.QueryFrozenParticipants(context.Background(), participants); err == nil {
		t.Fatal("invalid header")
	}
	if _, err = invalidHeader.QueryFrozenComments(context.Background(), comments); err == nil {
		t.Fatal("invalid header")
	}
}

func TestFrozenGroupTotalPreservesGroupSizeWhenSearchMatchesNoRows(t *testing.T) {
	boundary, _, collection := frozenBoundaryFixture(t)
	for _, entry := range []struct {
		group, query   string
		total, matched uint32
	}{{"included", "", 1, 1}, {"included", "no match", 1, 0}, {"excluded", "나", 1, 1}, {"unclassified", "", 1, 1}, {"", "", 3, 3}, {"", "USER1", 3, 1}} {
		response, err := boundary.QueryFrozenParticipants(context.Background(), contracts.FrozenParticipantsQuery{CollectionID: collection.CollectionID, Group: entry.group, Query: entry.query, Limit: 100})
		if err != nil || !response.OK || response.Data.Total != entry.total || response.Data.Matched != entry.matched || uint32(len(response.Data.Rows)) != entry.matched {
			t.Fatal(entry, response, err)
		}
	}
	after, err := boundary.GetCollection(context.Background(), contracts.CollectionQuery{CollectionID: collection.CollectionID})
	if err != nil || !after.OK || after.Data.Revision != collection.Revision {
		t.Fatal("readonly group queries changed collection", after, err)
	}
}

func (reader *frozenReadFault) ReadCollectionSnapshot(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {
	call := reader.calls.Add(1)
	if reader.continuous || call == reader.nth {
		return rl.FrozenCollection{Participants: []rl.ParticipantSnapshot{{ID: "partial-private"}}}, 1, errors.New("private storage fault")
	}
	return reader.ProductReader.ReadCollectionSnapshot(ctx, id)
}
func (reader frozenReadAlter) ReadCollectionSnapshot(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {
	frozen, revision, err := reader.ProductReader.ReadCollectionSnapshot(ctx, id)
	if err == nil {
		if reader.alter != nil {
			reader.alter(&frozen)
		}
		if reader.after != nil {
			reader.after()
		}
	}
	return frozen, revision, err
}

func TestFrozenParticipantProjectionThreePreviewsPreservesFullSnapshot(t *testing.T) {
	original := rl.ParticipantSnapshot{ID: "p", Kind: "fixed", Classification: "unclassified", Comments: []rl.CommentSnapshot{{Kind: "text", Text: strings.Repeat("a", 253) + "a\U0001e6e3xx"}, {Kind: "dccon"}, {Kind: "voice"}, {Kind: "text", Text: "fourth full body"}}}
	actual, err := frozenParticipantProjection(original)
	if err != nil || actual.CommentCount != 4 || !reflect.DeepEqual(actual.Previews, []string{strings.Repeat("a", 253) + "…", "[디시콘]", "[보플]"}) {
		t.Fatal(actual, err)
	}
	actual.Previews[0] = "changed"
	if original.Comments[0].Text != strings.Repeat("a", 253)+"a\U0001e6e3xx" || original.Comments[3].Text != "fourth full body" {
		t.Fatal("projection changed authoritative snapshot")
	}
	original.Comments = nil
	empty, err := frozenParticipantProjection(original)
	if err != nil || empty.Previews == nil || len(empty.Previews) != 0 {
		t.Fatal(empty, err)
	}
	original.Comments = []rl.CommentSnapshot{{Kind: "text", Text: "first"}, {Kind: "text", Text: string([]byte{0xff})}}
	invalid, err := frozenParticipantProjection(original)
	if err == nil || invalid.Previews != nil {
		t.Fatal("invalid text leaked partial previews", invalid, err)
	}
}
