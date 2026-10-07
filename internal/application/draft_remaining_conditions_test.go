package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/selection"
)

// These tests add only the A04/A09/A10/A11 conditions left by the source audit.
// The largest fixture contains 52 comments, not a performance workload.
func remainingConditionWaitWorker(t *testing.T, service *DraftService) {
	t.Helper()
	done := make(chan struct{})
	go func() { service.workers.Wait(); close(done) }()
	finalizeAwait(t, done)
}

func remainingConditionFault(t *testing.T, err error, expected contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != expected {
		t.Fatalf("safe fault = %v, want %s", err, expected)
	}
}

func remainingConditionOwnerImage(t *testing.T, service *DraftService) string {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	type receipt struct {
		Fingerprint [32]byte
		Data        contracts.DraftData
	}
	receipts := make(map[contracts.OperationID]receipt, len(service.operations))
	for id, entry := range service.operations {
		receipts[id] = receipt{Fingerprint: entry.fingerprint, Data: entry.data}
	}
	raw, err := json.Marshal(struct {
		Draft        contracts.DraftData
		Participants []selection.Participant
		Author       *selection.ParticipantKey
		Receipts     map[contracts.OperationID]receipt
		Order        []contracts.OperationID
		WorkerLease  bool
		Closed       bool
	}{
		Draft: service.draft, Participants: service.participants, Author: service.author,
		Receipts: receipts, Order: service.order, WorkerLease: service.cancel != nil, Closed: service.closed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDraftCommentsParentPointerAndIndependentPagingOperandsAreReadOnly(t *testing.T) {
	var calls, notices atomic.Int32
	service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, _ func(CollectionProgress)) (CollectionSnapshot, error) {
		calls.Add(1)
		snapshot := snapshotFixture()
		snapshot.Comments = snapshot.Comments[:1]
		for index := 0; index < 51; index++ {
			parent := "c1"
			snapshot.Comments = append(snapshot.Comments, selection.InputComment{
				ID: fmt.Sprintf("reply-%02d", index), ParentID: &parent, Nickname: "둘", Identifier: "target",
				ParticipantKind: selection.Fixed, Kind: selection.Text, Text: fmt.Sprintf("row-%02d", index),
				PostedAt: &draftNow,
			})
		}
		return snapshot, ctx.Err()
	}))
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "remaining-comments-load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	remainingConditionWaitWorker(t, service)
	draft := getDraft(t, service)
	participants, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: draft.Summary.DraftContext, Limit: 100})
	if err != nil || len(participants.Rows) != 2 || participants.Rows[1].PublicIdentifier != "target" || participants.Rows[1].CommentCount != 51 {
		t.Fatal("paging fixture must isolate the second participant's 51 comments", participants, err)
	}
	service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
	before := remainingConditionOwnerImage(t, service)
	targetID := participants.Rows[1].ID
	firstQuery := contracts.ParticipantCommentsQuery{DraftContext: draft.Summary.DraftContext, ParticipantID: targetID, Limit: 1}
	first, err := service.Comments(context.Background(), firstQuery)
	if err != nil || len(first.Rows) != 1 || first.Rows[0].ParentID == nil || *first.Rows[0].ParentID != "c1" {
		t.Fatal("fixture did not reach present ParentID copy", first, err)
	}
	*first.Rows[0].ParentID = "caller-owned"
	again, err := service.Comments(context.Background(), firstQuery)
	if err != nil || len(again.Rows) != 1 || again.Rows[0].ParentID == nil || *again.Rows[0].ParentID != "c1" || before != remainingConditionOwnerImage(t, service) {
		t.Fatal("returned ParentID aliases the participant or receipt owner", again, err)
	}
	for _, entry := range []struct {
		name          string
		offset, limit uint32
		start, count  int
	}{
		{name: "no-skip-cap-one", offset: 0, limit: 1, start: 0, count: 1},
		{name: "skip-first-cap-one", offset: 1, limit: 1, start: 1, count: 1},
		{name: "skip-first-full-fifty", offset: 1, limit: 50, start: 1, count: 50},
		{name: "cap-before-final-comment", offset: 0, limit: 50, start: 0, count: 50},
		{name: "last-comment", offset: 50, limit: 50, start: 50, count: 1},
		{name: "maximum-offset", offset: ^uint32(0), limit: 50, count: 0},
	} {
		t.Run(entry.name, func(t *testing.T) {
			page, err := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: draft.Summary.DraftContext, ParticipantID: targetID, Offset: entry.offset, Limit: entry.limit})
			if err != nil || page.Context != draft.Summary.DraftContext || page.ParticipantID != targetID || page.Total != 51 || page.Offset != entry.offset || page.Rows == nil || len(page.Rows) != entry.count {
				t.Fatal("offset/cap changed literal page metadata or row count", page, err)
			}
			for index, row := range page.Rows {
				ordinal := entry.start + index
				if row.ID != fmt.Sprintf("reply-%02d", ordinal) || row.Text != fmt.Sprintf("row-%02d", ordinal) || row.ParentID == nil || *row.ParentID != "c1" {
					t.Fatal("paging skipped/reordered another participant's comment", index, row)
				}
			}
			if before != remainingConditionOwnerImage(t, service) {
				t.Fatal("paging changed selection, counters, receipts, or worker lease")
			}
		})
	}
	missing, err := service.Comments(context.Background(), contracts.ParticipantCommentsQuery{DraftContext: draft.Summary.DraftContext, ParticipantID: "absent-but-valid-target", Limit: 1})
	remainingConditionFault(t, err, contracts.InvalidInput)
	if !reflect.DeepEqual(missing, contracts.CommentsPage{}) || before != remainingConditionOwnerImage(t, service) || calls.Load() != 1 || notices.Load() != 0 {
		t.Fatal("healthy missing-target query returned partial data or changed state/effects", missing)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDraftCreateCallerGuardsRejectIndependentlyWithoutConsumingReceipts(t *testing.T) {
	for _, name := range []string{"foreign-session", "nonzero-revision", "closed-owner", "changed-known-payload"} {
		t.Run(name, func(t *testing.T) {
			var collectionCalls, extraIDs, notices atomic.Int32
			service := newDraftHarness(t, testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
				collectionCalls.Add(1)
				return CollectionSnapshot{}, errors.New("Create cannot collect")
			}))
			service.options.NewID = func() string { extraIDs.Add(1); return "unexpected" }
			service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
			zero, one := contracts.Revision(0), contracts.Revision(1)
			healthy := contracts.CreateDraftRequest{MutationHeader: contracts.MutationHeader{
				ProtocolVersion: 1, BackendSessionID: "session", OperationID: "remaining-create", ExpectedRevision: &zero,
			}}
			bad, expectedCode := healthy, contracts.InvalidState
			switch name {
			case "foreign-session":
				bad.BackendSessionID, expectedCode = "other-session", contracts.BackendSessionChanged
			case "nonzero-revision":
				bad.ExpectedRevision, expectedCode = &one, contracts.StaleRevision
			case "closed-owner":
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
			case "changed-known-payload":
				healthy.OperationID, bad.OperationID = "create", "create"
				bad.ExpectedRevision, expectedCode = &one, contracts.InvalidInput // replay hash refusal precedes stale revision.
			}
			if err := bad.Validate(); err != nil {
				t.Fatal("entry fixture must pass the earlier wire validator", err)
			}
			before := remainingConditionOwnerImage(t, service)
			for repeat := 0; repeat < 2; repeat++ {
				value, err := service.Create(context.Background(), bad)
				remainingConditionFault(t, err, expectedCode)
				if !reflect.DeepEqual(value, contracts.DraftData{}) || before != remainingConditionOwnerImage(t, service) || extraIDs.Load() != 0 || collectionCalls.Load() != 0 || notices.Load() != 0 {
					t.Fatal("rejected caller changed owner/receipt/order, allocated a draft, or disclosed data", value)
				}
			}
			if name != "closed-owner" {
				prior := getDraft(t, service)
				corrected, err := service.Create(context.Background(), healthy)
				if err != nil || !reflect.DeepEqual(corrected, prior) || extraIDs.Load() != 0 || collectionCalls.Load() != 0 || notices.Load() != 0 {
					t.Fatal("same operation could not retain/reuse its original draft after correction", corrected, err)
				}
				operation, err := service.Operation(context.Background(), healthy.OperationID)
				if err != nil || operation.State != contracts.OperationSucceeded || operation.Summary == nil {
					t.Fatal("corrected caller lost its confirmed receipt", operation, err)
				}
				if name == "changed-known-payload" && before != remainingConditionOwnerImage(t, service) {
					t.Fatal("correct original payload replay replaced its stored receipt")
				}
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDraftCreateEmptyGeneratedIDLeavesOperationReusableAndOwnerEmpty(t *testing.T) {
	var ids, collectionCalls, notices atomic.Int32
	allowID := false
	service, err := NewDraftService(DraftOptions{
		Session: "session", Now: func() time.Time { return draftNow },
		NewID: func() string {
			ids.Add(1)
			if allowID {
				return "corrected-draft"
			}
			return ""
		},
		Collector: testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
			collectionCalls.Add(1)
			return CollectionSnapshot{}, errors.New("Create cannot collect")
		}),
		Publish: func(contracts.StateNotice) error { notices.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	zero := contracts.Revision(0)
	request := contracts.CreateDraftRequest{MutationHeader: contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: "session", OperationID: "empty-generated-id", ExpectedRevision: &zero}}
	before := remainingConditionOwnerImage(t, service)
	for repeat := 1; repeat <= 2; repeat++ {
		value, err := service.Create(context.Background(), request)
		remainingConditionFault(t, err, contracts.InvalidState)
		operation, readErr := service.Operation(context.Background(), request.OperationID)
		if !reflect.DeepEqual(value, contracts.DraftData{}) || readErr != nil || operation.State != contracts.OperationUnknown || operation.Summary != nil || service.Summary() != nil || before != remainingConditionOwnerImage(t, service) || ids.Load() != int32(repeat) {
			t.Fatal("empty generated ID admitted a draft/receipt or was silently retried", value, operation, readErr)
		}
	}
	allowID = true
	corrected, err := service.Create(context.Background(), request)
	if err != nil || corrected.Summary.DraftID != "corrected-draft" || corrected.Summary.State != contracts.DraftEmpty || corrected.Summary.Revision != 0 || corrected.Summary.ArticleGeneration != 0 || ids.Load() != 3 {
		t.Fatal("failed ID generation consumed the operation or wrapped initial counters", corrected, err)
	}
	after := remainingConditionOwnerImage(t, service)
	replay, err := service.Create(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replay, corrected) || after != remainingConditionOwnerImage(t, service) || ids.Load() != 3 || collectionCalls.Load() != 0 || notices.Load() != 0 {
		t.Fatal("same-ID replay allocated another draft or changed effects", replay, err)
	}
}

type remainingDCCollector func(context.Context, string, func(dcinside.Progress)) (dcinside.Snapshot, error)

func (collector remainingDCCollector) Collect(ctx context.Context, url string, progress func(dcinside.Progress)) (dcinside.Snapshot, error) {
	return collector(ctx, url, progress)
}

func remainingAdapterSnapshot() dcinside.Snapshot {
	articleAt, commentAt := draftNow.Add(-2*time.Hour), draftNow.Add(-time.Hour)
	parent := "dc-root"
	return dcinside.Snapshot{
		Article: dcinside.Article{
			Ref:   dcinside.ArticleRef{Kind: dcinside.Mini, GalleryID: "fixture", ArticleNo: "42", CanonicalURL: "https://gall.dcinside.com/mini/board/view/?id=fixture&no=42", RequestURL: "request-only"},
			Title: "<script>literal 한글</script>", GalleryName: "갤러리", PostedAt: &articleAt,
		},
		Comments: []dcinside.Comment{
			{ID: "dc-root", Nickname: "고정", Identifier: "fixed-id", ParticipantKind: "fixed", Kind: "text", Text: "literal & <img>", PostedAt: &commentAt},
			{ID: "dc-reply", ParentID: &parent, Nickname: "유동", Identifier: "1.2", ParticipantKind: "anonymous", Kind: "dccon", Text: "[디시콘]", MediaURLs: []string{"https://dcimg5.dcinside.com/fixture.png"}},
			{ID: "dc-voice", Nickname: "반고닉", Identifier: "semi-id", ParticipantKind: "semi_fixed", Kind: "voice", Text: "[보플]"},
		},
		CollectedAt: draftNow, Complete: true, Pages: 2, DeletedComments: 3, UnsupportedComments: 4,
	}
}

func remainingAdapterExpected() CollectionSnapshot {
	articleAt, commentAt := draftNow.Add(-2*time.Hour), draftNow.Add(-time.Hour)
	parent := "dc-root"
	return CollectionSnapshot{
		Article: contracts.ArticleData{URL: "https://gall.dcinside.com/mini/board/view/?id=fixture&no=42", Title: "<script>literal 한글</script>", GalleryID: "fixture", GalleryName: "갤러리", GalleryKind: "MI", Number: "42", PostedAt: &articleAt},
		Comments: []selection.InputComment{
			{ID: "dc-root", Nickname: "고정", Identifier: "fixed-id", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "literal & <img>", PostedAt: &commentAt},
			{ID: "dc-reply", ParentID: &parent, Nickname: "유동", Identifier: "1.2", ParticipantKind: selection.Anonymous, Kind: selection.Dccon, Text: "[디시콘]", MediaURLs: []string{"https://dcimg5.dcinside.com/fixture.png"}},
			{ID: "dc-voice", Nickname: "반고닉", Identifier: "semi-id", ParticipantKind: selection.SemiFixed, Kind: selection.Voice, Text: "[보플]"},
		},
		CollectedAt: draftNow, Pages: 2, Deleted: 3, Unsupported: 4,
	}
}

func TestDCCollectorAdapterAuthorAndProgressConditionsPreserveLiteralMapping(t *testing.T) {
	fixedID, anonymousID := "author-id", "1.9"
	for _, entry := range []struct {
		name             string
		author           *dcinside.Author
		expectedNickname string
		expectedID       *string
		expectedAuthor   *selection.ParticipantKey
		reportProgress   bool
	}{
		{name: "author-absent", reportProgress: true},
		{name: "author-unidentified", author: &dcinside.Author{Nickname: "작성자", ParticipantKind: "anonymous"}, expectedNickname: "작성자", reportProgress: true},
		{name: "fixed-identified", author: &dcinside.Author{Nickname: "작성자", Identifier: "author-id", ParticipantKind: "fixed"}, expectedNickname: "작성자", expectedID: &fixedID, expectedAuthor: &selection.ParticipantKey{Nickname: "작성자", Identifier: "author-id", Anonymous: false}, reportProgress: true},
		{name: "anonymous-identified", author: &dcinside.Author{Nickname: "작성자", Identifier: "1.9", ParticipantKind: "anonymous"}, expectedNickname: "작성자", expectedID: &anonymousID, expectedAuthor: &selection.ParticipantKey{Nickname: "작성자", Identifier: "1.9", Anonymous: true}, reportProgress: true},
		{name: "nil-progress-only"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source, expected := remainingAdapterSnapshot(), remainingAdapterExpected()
			source.Article.Author = entry.author
			expected.Article.AuthorNickname, expected.Article.AuthorIdentifier, expected.Author = entry.expectedNickname, entry.expectedID, entry.expectedAuthor
			sourceImage, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			var calls int
			adapter := DCCollectorAdapter{Collector: remainingDCCollector(func(actual context.Context, url string, callback func(dcinside.Progress)) (dcinside.Snapshot, error) {
				calls++
				if actual != ctx || url != "unchanged-input" || callback == nil {
					t.Fatal("adapter replaced context, URL, or progress port")
				}
				callback(dcinside.Progress{Page: 1, AcceptedComments: 2, DeletedComments: 90, UnsupportedComments: 91})
				callback(dcinside.Progress{Page: 2, AcceptedComments: 3, DeletedComments: 92, UnsupportedComments: 93})
				return source, nil
			})}
			observed := []CollectionProgress{}
			var progress func(CollectionProgress)
			if entry.reportProgress {
				progress = func(value CollectionProgress) { observed = append(observed, value) }
			}
			result, err := adapter.Collect(ctx, "unchanged-input", progress)
			wantProgress := []CollectionProgress{}
			if entry.reportProgress {
				wantProgress = []CollectionProgress{{Pages: 1, Comments: 2}, {Pages: 2, Comments: 3}}
			}
			after, marshalErr := json.Marshal(source)
			if err != nil || calls != 1 || !reflect.DeepEqual(result, expected) || !reflect.DeepEqual(observed, wantProgress) || marshalErr != nil || string(after) != string(sourceImage) {
				t.Fatal("author/progress condition changed literal metadata/comment mapping or input", result, observed, err)
			}
		})
	}
}

func TestDCCollectorAdapterNilAndIncompleteSnapshotNeverReturnPartialMapping(t *testing.T) {
	t.Run("nil-inner", func(t *testing.T) {
		var progressCalls int
		result, err := (DCCollectorAdapter{}).Collect(context.Background(), "example", func(CollectionProgress) { progressCalls++ })
		remainingConditionFault(t, err, contracts.InvalidState)
		if !reflect.DeepEqual(result, CollectionSnapshot{}) || progressCalls != 0 {
			t.Fatal("missing inner port returned partial data or progress", result)
		}
	})
	t.Run("actual-typed-nil-inner", func(t *testing.T) {
		var inner *dcinside.Collector
		result, err := (DCCollectorAdapter{Collector: inner}).Collect(context.Background(), "example", nil)
		var fault *dcinside.Error
		if !errors.As(err, &fault) || fault.Code != "InvalidCollectorDependencies" || !reflect.DeepEqual(result, CollectionSnapshot{}) {
			t.Fatal("actual typed nil dependency lost its safe fault or returned partial data", result, err)
		}
	})
	t.Run("incomplete-only", func(t *testing.T) {
		snapshot := remainingAdapterSnapshot()
		snapshot.Complete = false
		var calls int
		adapter := DCCollectorAdapter{Collector: remainingDCCollector(func(context.Context, string, func(dcinside.Progress)) (dcinside.Snapshot, error) {
			calls++
			return snapshot, nil
		})}
		result, err := adapter.Collect(context.Background(), "example", nil)
		remainingConditionFault(t, err, contracts.InvalidState)
		if calls != 1 || !reflect.DeepEqual(result, CollectionSnapshot{}) {
			t.Fatal("incomplete source returned mapped comments or was retried", result, calls)
		}
	})
}

func TestDCCollectorAdapterFirstNthContinuousAndContextFailuresKeepOriginalCauseAndNoPartialResult(t *testing.T) {
	sentinel := errors.New("owned adapter dependency failure")
	for _, scenario := range []struct {
		name      string
		failCalls map[int]bool
		cause     error
		cancelled bool
		deadline  bool
	}{
		{name: "first", failCalls: map[int]bool{1: true}, cause: sentinel},
		{name: "nth-after-success", failCalls: map[int]bool{2: true}, cause: sentinel},
		{name: "continuous", failCalls: map[int]bool{1: true, 2: true, 3: true}, cause: sentinel},
		{name: "cancelled", failCalls: map[int]bool{1: true}, cause: context.Canceled, cancelled: true},
		{name: "deadline", failCalls: map[int]bool{1: true}, cause: context.DeadlineExceeded, deadline: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario.cancelled {
				cancel()
			}
			if scenario.deadline {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(context.Background(), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
				defer deadlineCancel()
			}
			calls, progressCalls := 0, 0
			adapter := DCCollectorAdapter{Collector: remainingDCCollector(func(actual context.Context, _ string, callback func(dcinside.Progress)) (dcinside.Snapshot, error) {
				calls++
				if actual != ctx || ((scenario.cancelled || scenario.deadline) && actual.Err() != scenario.cause) {
					t.Fatal("adapter did not preserve cancelled/deadline context")
				}
				callback(dcinside.Progress{Page: 1, AcceptedComments: 3})
				partial := remainingAdapterSnapshot()
				if scenario.failCalls[calls] {
					partial.Complete = false // dependency error must precede the incomplete guard.
					return partial, scenario.cause
				}
				return partial, nil
			})}
			lastCall := 4
			if scenario.cancelled || scenario.deadline {
				lastCall = 1
			}
			for ordinal := 1; ordinal <= lastCall; ordinal++ {
				result, err := adapter.Collect(ctx, "unchanged-input", func(value CollectionProgress) {
					progressCalls++
					if value != (CollectionProgress{Pages: 1, Comments: 3}) {
						t.Fatal("failure progress changed metadata", value)
					}
				})
				if scenario.failCalls[ordinal] {
					if err != scenario.cause || !reflect.DeepEqual(result, CollectionSnapshot{}) {
						t.Fatal("dependency failure lost cause, disclosed partial snapshot, or was fabricated", result, err)
					}
				} else if err != nil || !reflect.DeepEqual(result, remainingAdapterExpected()) {
					t.Fatal("successful call around failure returned altered mapping", result, err)
				}
				if calls != ordinal || progressCalls != ordinal {
					t.Fatal("adapter silently retried or duplicated progress", calls, progressCalls, ordinal)
				}
			}
		})
	}
}

func TestDraftNonzeroUTCYearZeroCollectionFailsOnlyLowerYearGuardAndKeepsConfiguredOwner(t *testing.T) {
	service, old := configuredDraftSnapshot(t)
	oldSelection := promotionAuditOwnerJSON(t, service)
	invalidTime := time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC)
	_, offset := invalidTime.Zone()
	if invalidTime.IsZero() || offset != 0 || invalidTime.Year() != 0 {
		t.Fatal("year-zero fixture must isolate lower-year operand")
	}
	var calls, notices atomic.Int32
	corrected := false
	service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
	service.options.Collector = testCollector(func(ctx context.Context, _ string, _ func(CollectionProgress)) (CollectionSnapshot, error) {
		calls.Add(1)
		snapshot := snapshotFixture()
		snapshot.Article.Title = "year-boundary replacement"
		snapshot.CollectedAt = invalidTime
		if corrected {
			snapshot.CollectedAt = time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC)
		}
		return snapshot, ctx.Err()
	})
	request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "nonzero-year-zero"), URL: "example"}
	if _, err := service.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	remainingConditionWaitWorker(t, service)
	failed := getDraft(t, service)
	if failed.Load == nil || failed.Load.State != "failed" || failed.Load.FailureCode == nil || *failed.Load.FailureCode != contracts.InvalidState || failed.Summary.ArticleGeneration != old.Summary.ArticleGeneration || failed.Summary.Revision != old.Summary.Revision+2 {
		t.Fatal("nonzero UTC year zero bypassed lower-bound validation", failed)
	}
	assertPreviousCollectionPreserved(t, service, old)
	if promotionAuditOwnerJSON(t, service) != oldSelection {
		t.Fatal("invalid collection time replaced participant/manual/author owner")
	}
	beforeReplay := remainingConditionOwnerImage(t, service)
	replay, err := service.Load(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replay, failed) || beforeReplay != remainingConditionOwnerImage(t, service) || calls.Load() != 1 || notices.Load() != 2 {
		t.Fatal("failed same-ID replay retried collection or changed owner", replay, err)
	}
	service.mu.Lock()
	leaseReleased := service.cancel == nil
	service.mu.Unlock()
	if !leaseReleased {
		t.Fatal("year-zero rejection retained collector lease")
	}
	corrected = true
	if _, err := service.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "minimum-year-corrected"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	remainingConditionWaitWorker(t, service)
	complete := getDraft(t, service)
	if complete.Load.State != "completed" || complete.Summary.Snapshot == nil || complete.Summary.Snapshot.CollectedAt.Year() != 1 || complete.Summary.Snapshot.CollectedAt.IsZero() || complete.Summary.ArticleGeneration != old.Summary.ArticleGeneration+1 || complete.Summary.Revision != old.Summary.Revision+4 || complete.Article.Title != "year-boundary replacement" || !reflect.DeepEqual(complete.Filters, old.Filters) || !reflect.DeepEqual(complete.Prizes, old.Prizes) || calls.Load() != 2 || notices.Load() != 4 {
		t.Fatal("lower-bound correction could not promote exactly once", complete, err)
	}
	service.mu.Lock()
	leaseReleased = service.cancel == nil
	service.mu.Unlock()
	if !leaseReleased {
		t.Fatal("corrected collection retained worker lease")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}
