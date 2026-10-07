// Permanent public draft boundary regressions; production owner remains unchanged.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

// A03: each pointer is mutated independently through public Get and Load replay.
// The public Edit caller's TimeCut pointer is also detached at commit. No fake
// dependency mutates an article after transferring its collected snapshot.
func TestDraftPublicProjectionsDetachEachOptionalPointee(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*contracts.DraftData)
	}{
		{"filter-timecut", func(data *contracts.DraftData) { *data.Filters.TimeCut = draftNow.Add(4 * time.Hour) }},
		{"article-posted-at", func(data *contracts.DraftData) { *data.Article.PostedAt = draftNow.Add(5 * time.Hour) }},
		{"article-author-identifier", func(data *contracts.DraftData) { *data.Article.AuthorIdentifier = "caller-owned-replacement" }},
	}
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			var calls, notices atomic.Int32
			service := newDraftHarness(t, testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
				calls.Add(1)
				progress(CollectionProgress{Pages: 1, Comments: 2})
				snapshot := snapshotFixture()
				posted, identifier := draftNow.Add(-time.Hour), "article-public-author"
				snapshot.Article.PostedAt, snapshot.Article.AuthorIdentifier = &posted, &identifier
				return snapshot, ctx.Err()
			}))
			service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
			load := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(service, "pointer-load"), URL: "example"}
			if _, err := service.Load(context.Background(), load); err != nil {
				t.Fatal(err)
			}
			service.workers.Wait()
			cut := draftNow.Add(time.Minute)
			filters := getDraft(t, service).Filters
			filters.TimeCut = &cut
			if _, err := service.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "pointer-filter"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
				t.Fatal(err)
			}
			expected := getDraft(t, service)
			// Capture values before changing any returned pointee. A broken
			// clone must not make both expected and owner change together.
			expectedJSON, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			assertOwnerUnchanged := func(reason string) {
				t.Helper()
				actualJSON, err := json.Marshal(getDraft(t, service))
				if err != nil || string(actualJSON) != string(expectedJSON) {
					t.Fatal(reason, err)
				}
			}
			if expected.Filters.TimeCut == nil || expected.Article == nil || expected.Article.PostedAt == nil || expected.Article.AuthorIdentifier == nil {
				t.Fatal("fixture did not reach all present-pointer branches")
			}
			if entry.name == "filter-timecut" {
				cut = draftNow.Add(8 * time.Hour)
				assertOwnerUnchanged("public Edit caller pointee aliases owner")
			}
			projections := []struct {
				name string
				read func() contracts.DraftData
			}{
				{"Get", func() contracts.DraftData { return getDraft(t, service) }},
				{"same-ID-Load-replay", func() contracts.DraftData {
					data, err := service.Load(context.Background(), load)
					if err != nil {
						t.Fatal("same-ID read replay rejected", err)
					}
					return data
				}},
			}
			for _, projection := range projections {
				data := projection.read()
				if !reflect.DeepEqual(expected, data) {
					t.Fatal("read projection changed baseline", projection.name)
				}
				entry.alter(&data)
				assertOwnerUnchanged("returned pointee aliases draft owner: " + projection.name)
			}
			if calls.Load() != 1 || notices.Load() != 3 {
				t.Fatal("read/replay created another worker or notice", calls.Load(), notices.Load())
			}
			service.mu.Lock()
			leaseReleased := service.cancel == nil
			service.mu.Unlock()
			if !leaseReleased {
				t.Fatal("completed pointer fixture retained a worker lease")
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A05: query length is the production byte limit, not rune or UTF16 length.
// Search operand independence uses literal nickname-only/identifier-only values.
func TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands(t *testing.T) {
	service := completedDraft(t)
	before := getDraft(t, service)
	cases := []struct {
		name, query, group string
		identifiers        []string
		rejected           bool
	}{
		{"empty-search", "", "", []string{"user1", "public-ip"}, false},
		{"nickname-only", "가", "", []string{"user1"}, false},
		{"identifier-only-ignore-case", "PUBLIC-IP", "", []string{"public-ip"}, false},
		{"neither-search-operand", "missing", "", []string{}, false},
		{"ascii-1000-bytes", strings.Repeat("q", 1000), "", []string{}, false},
		{"ascii-1001-bytes", strings.Repeat("q", 1001), "", nil, true},
		{"unicode-1000-bytes", strings.Repeat("가", 333) + "a", "", []string{}, false},
		{"unicode-1001-bytes", strings.Repeat("가", 333) + "ab", "", nil, true},
		{"unknown-group-only", "", "other", nil, true},
	}
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			page, err := service.Participants(context.Background(), contracts.ParticipantQuery{DraftContext: before.Summary.DraftContext, Query: entry.query, Group: entry.group, Limit: 100})
			if entry.rejected {
				var fault contracts.Fault
				if !errors.As(err, &fault) || fault.Code != contracts.InvalidInput || !reflect.DeepEqual(page, contracts.ParticipantsPage{}) {
					t.Fatal("invalid query returned rows or a wrong error", page, err)
				}
			} else {
				identifiers := make([]string, len(page.Rows))
				for index, row := range page.Rows {
					identifiers[index] = row.PublicIdentifier
				}
				if err != nil || page.Context != before.Summary.DraftContext || page.Total != 2 || page.Matched != uint32(len(entry.identifiers)) || page.Offset != 0 || page.Rows == nil || !reflect.DeepEqual(identifiers, entry.identifiers) {
					t.Fatal("literal query/group/search oracle failed", page, err)
				}
			}
			if !reflect.DeepEqual(before, getDraft(t, service)) {
				t.Fatal("query changed confirmed counters or owner")
			}
		})
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

// A06: the typed public request can carry year10000. It passes the embedded
// header validator, then json.Marshal fails before a receipt or state change.
// Correcting that same, never-admitted operation ID must remain legal.
func TestDraftEditUnrepresentableTimeRefusesBeforeAdmissionAndSameIDCanBeCorrected(t *testing.T) {
	service := completedDraft(t)
	before := getDraft(t, service)
	var notices, unexpectedCalls atomic.Int32
	service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
	service.options.Collector = testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
		unexpectedCalls.Add(1)
		return CollectionSnapshot{}, errors.New("query/edit cannot collect")
	})
	service.mu.Lock()
	beforeOrder, beforeCount := append([]contracts.OperationID{}, service.order...), len(service.operations)
	service.mu.Unlock()
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	filters := before.Filters
	filters.TimeCut = &invalid
	request := contracts.DraftEditRequest{DraftMutationHeader: draftHeader(service, "unrepresentable-time"), Kind: "UpdateFilters", Filters: &filters}
	if err := request.Validate(); err != nil {
		t.Fatal("fixture was rejected by a different earlier guard", err)
	}
	result, err := service.Edit(context.Background(), request)
	var marshalFailure *json.MarshalerError
	if !errors.As(err, &marshalFailure) || !reflect.DeepEqual(result, contracts.DraftData{}) {
		t.Fatal("unrepresentable time did not fail at public hash before admission", result, err)
	}
	operation, err := service.Operation(context.Background(), request.OperationID)
	if err != nil || operation.State != contracts.OperationUnknown || operation.Summary != nil || operation.FailureCode != nil {
		t.Fatal("hash failure consumed or invented a receipt", operation, err)
	}
	service.mu.Lock()
	unchangedReceipts := len(service.operations) == beforeCount && reflect.DeepEqual(service.order, beforeOrder) && service.cancel == nil
	service.mu.Unlock()
	if !unchangedReceipts || !reflect.DeepEqual(before, getDraft(t, service)) || notices.Load() != 0 || unexpectedCalls.Load() != 0 {
		t.Fatal("hash failure changed owner, counters, worker, or published a notice")
	}
	valid := draftNow.Add(time.Minute)
	filters.TimeCut = &valid
	expected, err := service.Edit(context.Background(), request)
	if err != nil || expected.Summary.Revision != before.Summary.Revision+1 || expected.Summary.ArticleGeneration != before.Summary.ArticleGeneration || expected.Filters.TimeCut == nil || !expected.Filters.TimeCut.Equal(valid) {
		t.Fatal("same never-admitted ID could not be corrected", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		replay, err := service.Edit(context.Background(), request)
		if err != nil || !reflect.DeepEqual(expected, replay) {
			t.Fatal("corrected same-ID replay mutated owner", err)
		}
	}
	if notices.Load() != 1 || unexpectedCalls.Load() != 0 {
		t.Fatal("corrected/replayed edit duplicated effects")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func publicBoundaryOwnerImage(t *testing.T, service *DraftService) string {
	t.Helper()
	service.mu.Lock()
	raw, err := json.Marshal(struct {
		Draft        contracts.DraftData
		Order        []contracts.OperationID
		ReceiptCount int
		WorkerLease  bool
	}{service.draft, service.order, len(service.operations), service.cancel != nil})
	service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A08: nil context, cancelled context, empty ID and closed owner are independent;
// earlier guards are otherwise valid in each case. Closed is entered by public Close.
func TestDraftOperationReadIndependentGuardFailuresHaveNoEffects(t *testing.T) {
	for _, name := range []string{"nil-context", "cancelled-context", "empty-id", "closed-owner"} {
		t.Run(name, func(t *testing.T) {
			service := completedDraft(t)
			before := publicBoundaryOwnerImage(t, service)
			var notices, unexpectedCalls atomic.Int32
			service.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
			service.options.Collector = testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
				unexpectedCalls.Add(1)
				return CollectionSnapshot{}, errors.New("read cannot collect")
			})
			ctx, id := context.Context(context.Background()), contracts.OperationID("load")
			cancelled := false
			switch name {
			case "nil-context":
				ctx = nil
			case "cancelled-context":
				current, cancel := context.WithCancel(context.Background())
				cancel()
				ctx, cancelled = current, true
			case "empty-id":
				id = ""
			case "closed-owner":
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
			}
			value, err := service.Operation(ctx, id)
			if !reflect.DeepEqual(value, contracts.DraftOperationData{}) {
				t.Fatal("rejected read disclosed a partial receipt", value)
			}
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancel cause changed", err)
				}
			} else {
				expectedCode := contracts.InvalidInput
				if name == "closed-owner" {
					expectedCode = contracts.InvalidState
				}
				var fault contracts.Fault
				if !errors.As(err, &fault) || fault.Code != expectedCode {
					t.Fatal("independent guard failure changed safe code", err)
				}
			}
			if before != publicBoundaryOwnerImage(t, service) || notices.Load() != 0 || unexpectedCalls.Load() != 0 {
				t.Fatal("rejected read changed state/order/receipts, lease, or effects")
			}
			if name != "closed-owner" {
				known, err := service.Operation(context.Background(), "load")
				if err != nil || known.State != contracts.OperationSucceeded || known.Summary == nil {
					t.Fatal("rejected read retired known receipt", err)
				}
				unknown, err := service.Operation(context.Background(), "never-admitted")
				if err != nil || unknown.State != contracts.OperationUnknown || unknown.Summary != nil || unknown.FailureCode != nil {
					t.Fatal("unknown read invented failure", err)
				}
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
