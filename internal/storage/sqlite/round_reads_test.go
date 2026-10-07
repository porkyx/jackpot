package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func nullString(raw string) sql.NullString { return sql.NullString{String: raw, Valid: true} }
func emptyNullString() sql.NullString      { return sql.NullString{} }

func TestCommittedTypedReadsAreStableFreshAndKeepVerifierPrivate(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	var reader rl.ReadStorage = store
	collection, err := reader.ReadCollection(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Participants) != 3 || collection.Participants[0].ID != "p1" || collection.Participants[2].Included {
		t.Fatalf("snapshot lost: %+v", collection)
	}
	raw := jsonValue(t, collection)
	if strings.Contains(raw, `"Credential"`) || strings.Contains(raw, `"Salt"`) || strings.Contains(raw, `"Key"`) {
		t.Fatal("private verifier serialized with snapshot")
	}
	collection.Participants[0].Nickname = "changed"
	collection.Participants[0].Comments[0].Text = "changed"

	second, err := reader.ReadCollection(context.Background(), "c1")
	if err != nil || second.Participants[0].Nickname == "changed" || second.Participants[0].Comments[0].Text == "changed" {
		t.Fatalf("read aliased/mutated storage: %+v/%v", second, err)
	}
	round, err := reader.ReadRound(context.Background(), "r1")
	if err != nil || round.State != contracts.Executing || round.Outcome != nil || round.Claim != nil {
		t.Fatalf("uncommitted outcome: %+v/%v", round, err)
	}
	round.Input.CandidateIDs[0] = "changed"
	round.Input.Prizes[0].Name = "changed"
	again, err := reader.ReadRound(context.Background(), "r1")
	if err != nil || again.Input.CandidateIDs[0] != "p1" || again.Input.Prizes[0].Name != "상품" {
		t.Fatal("round read mutated immutable input")
	}
	operation, err := reader.ReadOperation(context.Background(), "op1")
	if err != nil || operation.Status != contracts.OperationPending || operation.Revision != 1 {
		t.Fatalf("operation lost: %+v/%v", operation, err)
	}
	if _, err := reader.ReadOperation(context.Background(), "op1"); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := store.db.QueryRow("SELECT revision FROM collections WHERE id='c1'").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("read advanced revision: %d/%v", revision, err)
	}
	assertNoHandles(t, store)
}

func TestReadOperationUnknownRequiresSuccessfulDatabaseQuery(t *testing.T) {
	store := migratedTestStore(t)
	record, err := store.ReadOperation(context.Background(), "absent")
	if err != nil || record.Status != contracts.OperationUnknown || record.Operation.ID != "absent" {
		t.Fatalf("unknown: %+v/%v", record, err)
	}
	if _, err := store.ReadRound(context.Background(), "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := store.ReadCollection(context.Background(), "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	record, err = store.ReadOperation(context.Background(), "absent")
	if err == nil || record.Status == contracts.OperationUnknown || !reflect.DeepEqual(record, rl.OperationRecord{}) {
		t.Fatalf("storage failure became unknown: %+v/%v", record, err)
	}
}

func TestAllStoredReadsRejectNilEmptyCancellationDeadlineAndClosedDBWithoutPartialSuccess(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "cancelled", "deadline", "closed"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			ctx := context.Background()
			c, r, op := "c1", "r1", "op1"
			var want error
			switch mode {
			case "nil":
				ctx = nil
			case "empty":
				c, r, op = "", "", ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Time{})
				defer cancel()
				want = context.DeadlineExceeded
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			collection, err := store.ReadCollection(ctx, contracts.CollectionID(c))
			if err == nil || want != nil && !errors.Is(err, want) || !reflect.DeepEqual(collection, rl.FrozenCollection{}) {
				t.Fatalf("partial collection: %+v/%v", collection, err)
			}
			round, err := store.ReadRound(ctx, contracts.RoundID(r))
			if err == nil || want != nil && !errors.Is(err, want) || !reflect.DeepEqual(round, rl.RoundRecord{}) {
				t.Fatalf("partial round: %+v/%v", round, err)
			}
			operation, err := store.ReadOperation(ctx, contracts.OperationID(op))
			if err == nil || want != nil && !errors.Is(err, want) || !reflect.DeepEqual(operation, rl.OperationRecord{}) {
				t.Fatalf("partial operation: %+v/%v", operation, err)
			}
			assertNoHandles(t, store)
		})
	}
}

func TestStoredDecoderRejectsUnknownFieldsTrailingValuesMalformedAndOversizedData(t *testing.T) {
	for _, raw := range []string{"", "null", "null null", "{} {}", `{"unknown":true}`, "bad", `{"Message":"` + strings.Repeat("x", 100<<20) + `"}`} {
		var input rl.RoundInput
		if err := decodeStored(raw, &input); err == nil {
			t.Fatal("corrupt stored input accepted")
		}
	}
	var input rl.RoundInput
	if err := decodeStored("{} \n", &input); err != nil {
		t.Fatal(err)
	}
}

func TestStoredUTCRejectsZeroMalformedOffsetAndOverflowAndPreservesAbsence(t *testing.T) {
	for _, raw := range []string{"", "bad", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "2026-10-06T14:00:00+09:00", "10000-01-01T00:00:00Z"} {
		value, err := storedTime(nullString(raw))
		if value != nil || err == nil {
			t.Fatalf("%q: %v/%v", raw, value, err)
		}
	}
	value, err := storedTime(nullString("2026-10-06T05:00:00.123456789Z"))
	if err != nil || value == nil || value.Nanosecond() != 123456789 {
		t.Fatalf("nanoseconds lost: %v/%v", value, err)
	}
	value, err = storedTime(emptyNullString())
	if err != nil || value != nil {
		t.Fatal("absent timestamp became a zero value")
	}
}

func TestSQLReadFaultAdapterFirstNthAndContinuousFailureDoesNotInventData(t *testing.T) {
	for _, failureAt := range []int{1, 3, 0} {
		t.Run(fmt.Sprint(failureAt), func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			sentinel := errors.New("injected read failure")
			var reader rl.ReadStorage = &readFault{ReadStorage: store, err: sentinel, at: failureAt}
			for i := 1; i <= 4; i++ {
				result, err := reader.ReadOperation(context.Background(), "op1")
				failed := failureAt == 0 || i == failureAt
				if failed {
					if !errors.Is(err, sentinel) || !reflect.DeepEqual(result, rl.OperationRecord{}) {
						t.Fatalf("failure invented data: %+v/%v", result, err)
					}
				} else if err != nil || result.Status != contracts.OperationPending {
					t.Fatalf("read dropped: %+v/%v", result, err)
				}
			}
			original, err := store.ReadOperation(context.Background(), "op1")
			if err != nil || original.Revision != 1 || original.Status != contracts.OperationPending {
				t.Fatal("fault adapter changed original")
			}
			assertNoHandles(t, store)
		})
	}
}

type readFault struct {
	rl.ReadStorage
	at, calls int
	err       error
}

func (f *readFault) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	f.calls++
	if f.at == 0 || f.at == f.calls {
		return rl.OperationRecord{}, f.err
	}
	return f.ReadStorage.ReadOperation(ctx, id)
}

func FuzzStoredRoundDecoder(f *testing.F) {
	for _, seed := range []string{"{}", "", `{"CandidateIDs":["p1"],"Prizes":[],"Mode":"immediate"}`, "null", "{} {}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<20 {
			return
		}
		var input rl.RoundInput
		if err := decodeStored(raw, &input); err == nil {
			canonical := jsonValue(t, input)
			var decoded rl.RoundInput
			if err := decodeStored(canonical, &decoded); err != nil || !reflect.DeepEqual(input, decoded) {
				t.Fatal("decode/encode invariant")
			}
		}
	})
}

// Disable only temporary-test constraints to simulate externally corrupted DBs.
// Product readers must fail loudly even when SQLite constraints were bypassed.
func corruptionMode(t *testing.T, store *Store) {
	execSQL(t, store.db, `PRAGMA ignore_check_constraints=ON; DROP TRIGGER immutable_operation_key; DROP TRIGGER immutable_terminal_operation; DROP TRIGGER immutable_round_input; DROP TRIGGER valid_round_transition; DROP TRIGGER immutable_terminal_round; DROP TRIGGER immutable_collection; DROP TRIGGER immutable_participant; DROP TRIGGER immutable_result`)
}
func TestOperationReadRejectsEveryCorruptMetadataFieldWithoutPartialData(t *testing.T) {
	cases := map[string]string{
		"kind":                   `UPDATE operations SET kind='unknown'`,
		"revision":               `UPDATE operations SET revision=9007199254740992`,
		"negative revision scan": `UPDATE operations SET revision=-1`,
		"fingerprint":            `UPDATE operations SET public_fingerprint=zeroblob(31)`,
		"status":                 `UPDATE operations SET status='unknown'`,
		"pending failure code":   `UPDATE operations SET failure_code='InvalidInput'`,
		"succeeded failure code": `UPDATE operations SET status='succeeded',failure_code='InvalidInput'`,
		"failed unknown code":    `UPDATE operations SET status='failed',failure_code='unknown'`,
		"failed empty code":      `UPDATE operations SET status='failed'`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			corruptionMode(t, store)
			execSQL(t, store.db, query)
			record, err := store.ReadOperation(context.Background(), "op1")
			if err == nil || !reflect.DeepEqual(record, rl.OperationRecord{}) {
				t.Fatalf("corrupt metadata escaped: %+v/%v", record, err)
			}
			assertNoHandles(t, store)
		})
	}
	for _, status := range []string{"pending", "succeeded", "failed"} {
		t.Run("valid "+status, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			code := ""
			if status == "failed" {
				code = "InvalidState"
			}
			execSQL(t, store.db, "UPDATE operations SET status=?,failure_code=?", status, code)
			record, err := store.ReadOperation(context.Background(), "op1")
			if err != nil || string(record.Status) != status || string(record.FailureCode) != code {
				t.Fatalf("valid status lost: %+v/%v", record, err)
			}
		})
	}
}

func TestRoundReadRejectsIndependentCorruptStateVersionClaimAndOutcomeFields(t *testing.T) {
	cases := map[string]string{
		"state":                     `UPDATE rounds SET state='unknown'`,
		"version high":              `UPDATE rounds SET version=9007199254740992`,
		"version zero":              `UPDATE rounds SET version=0`,
		"version scan":              `UPDATE rounds SET version=-1`,
		"revision high":             `UPDATE collections SET revision=9007199254740992`,
		"revision zero":             `UPDATE collections SET revision=0`,
		"revision scan":             `UPDATE collections SET revision=-1`,
		"number zero":               `UPDATE rounds SET number=0`,
		"attempt zero":              `UPDATE rounds SET attempt=0`,
		"input JSON":                `UPDATE rounds SET input_json='bad'`,
		"input unknown field":       `UPDATE rounds SET input_json='{"secret":true}'`,
		"input null":                `UPDATE rounds SET input_json='null'`,
		"schedule time":             `UPDATE rounds SET scheduled_at='bad'`,
		"claim JSON":                `UPDATE rounds SET claim_json='{"secret":true}'`,
		"completed without outcome": `UPDATE rounds SET state='completed'`,
		"outcome before completed":  `INSERT INTO results VALUES ('c1','r1','{}')`,
		"outcome JSON":              `INSERT INTO results VALUES ('c1','r1','bad')`,
		"outcome null":              `INSERT INTO results VALUES ('c1','r1','null')`,
		"failed empty code":         `UPDATE rounds SET state='failed'`,
		"failed unknown code":       `UPDATE rounds SET state='failed',failure_code='unknown'`,
		"executing failure code":    `UPDATE rounds SET failure_code='InvalidState'`,
		"scheduled without time":    `UPDATE rounds SET state='scheduled',timezone='Asia/Seoul'`,
		"scheduled wrong timezone":  `UPDATE rounds SET state='scheduled',scheduled_at='2026-10-06T05:00:00Z',timezone='wrong'`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			corruptionMode(t, store)
			execSQL(t, store.db, query)
			record, err := store.ReadRound(context.Background(), "r1")
			if err == nil || !reflect.DeepEqual(record, rl.RoundRecord{}) {
				t.Fatalf("corrupt round escaped: %+v/%v", record, err)
			}
			assertNoHandles(t, store)
		})
	}
	valid := rl.ClaimToken{CollectionID: "c1", RoundID: "r1", OperationID: "op1", Session: "session", Attempt: 1, Version: 1}
	for _, field := range []string{"collection", "round", "operation empty", "operation other", "session", "attempt", "version zero", "version high"} {
		t.Run("claim "+field, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			token := valid
			switch field {
			case "collection":
				token.CollectionID = "other"
			case "round":
				token.RoundID = "other"
			case "operation empty":
				token.OperationID = ""
			case "operation other":
				token.OperationID = "other"
			case "session":
				token.Session = ""
			case "attempt":
				token.Attempt = 2
			case "version zero":
				token.Version = 0
			case "version high":
				token.Version = 2
			}
			execSQL(t, store.db, "UPDATE rounds SET claim_json=?", jsonValue(t, token))
			record, err := store.ReadRound(context.Background(), "r1")
			if err == nil || !reflect.DeepEqual(record, rl.RoundRecord{}) {
				t.Fatalf("invalid claim escaped: %+v/%v", record, err)
			}
		})
	}
	for _, mode := range []string{"claim", "failed", "scheduled"} {
		t.Run("valid "+mode, func(t *testing.T) {
			store := migratedTestStore(t)
			id := contracts.RoundID("r1")
			if mode != "scheduled" {
				seedRound(t, store, "c1", "r1", "op1", "executing")
			}
			switch mode {
			case "claim":
				if _, err := store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: "c1", RoundID: id, OperationID: "op1", ExpectedRevision: 1, ExpectedVersion: 1, Attempt: 1, Session: "session-one"}); err != nil {
					t.Fatal(err)
				}
			case "failed":
				if _, err := store.RecordAttemptFailure(context.Background(), rl.AttemptFailureRequest{CollectionID: "c1", RoundID: id, OperationID: "op1", ExpectedRevision: 1, ExpectedVersion: 1, Attempt: 1, Code: contracts.InvalidState, FailedAt: storageNow}); err != nil {
					t.Fatal(err)
				}
			case "scheduled":
				service := productService(t, store, new(productEntropy), func() time.Time { return storageNow }, "session-one", nil)
				pending, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
				if err != nil {
					t.Fatal(err)
				}
				delay := uint32(30)
				scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "valid-schedule", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
				if err != nil {
					t.Fatal(err)
				}
				id = scheduled.ID
			}
			if _, err := store.ReadRound(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			assertNoHandles(t, store)
		})
	}
}
func TestFrozenCollectionReadRejectsMetadataVerifierAndPartialParticipantCorruption(t *testing.T) {
	cases := map[string]string{
		"metadata malformed":                   `UPDATE collections SET frozen_json='bad'`,
		"metadata unknown field":               `UPDATE collections SET frozen_json='{"secret":true}'`,
		"metadata wrong ID":                    `UPDATE collections SET frozen_json=json_set(frozen_json,'$.ID','other')`,
		"metadata wrong source":                `UPDATE collections SET frozen_json=json_set(frozen_json,'$.SourceDraftID','other')`,
		"metadata duplicated participants":     `UPDATE collections SET frozen_json=json_set(frozen_json,'$.Participants',json('[]'))`,
		"participant malformed after good row": `UPDATE participants SET body_json='bad' WHERE id='p2'`,
		"participant wrong ID":                 `UPDATE participants SET body_json=json_set(body_json,'$.ID','other') WHERE id='p2'`,
		"participant wrong inclusion":          `UPDATE participants SET body_json=json_set(body_json,'$.Included',json('false')) WHERE id='p2'`,
		"participant boolean scan":             `UPDATE participants SET included=2 WHERE id='p2'`,
		"missing participant table":            `DROP TABLE round_candidates; DROP TABLE winners; DROP TABLE participants`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			corruptionMode(t, store)
			execSQL(t, store.db, query)
			record, err := store.ReadCollection(context.Background(), "c1")
			if err == nil || !reflect.DeepEqual(record, rl.FrozenCollection{}) {
				t.Fatalf("partial/corrupt collection escaped: %+v/%v", record, err)
			}
			assertNoHandles(t, store)
		})
	}
}

func TestFrozenCollectionParticipantCountAtLimitAndLimitPlusOne(t *testing.T) {
	for _, count := range []int{100000, 100001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			execSQL(t, store.db, `WITH RECURSIVE numbers(n) AS (VALUES(4) UNION ALL SELECT n+1 FROM numbers WHERE n<?) INSERT INTO participants SELECT 'c1','extra-'||n,n-1,1,json_object('ID','extra-'||n,'Included',json('true')) FROM numbers`, count)
			record, err := store.ReadCollection(context.Background(), "c1")
			if count == 100000 {
				if err != nil || len(record.Participants) != count {
					t.Fatalf("exact bound refused: %d/%v", len(record.Participants), err)
				}
			} else if err == nil || !reflect.DeepEqual(record, rl.FrozenCollection{}) {
				t.Fatal("oversized read leaked partial data")
			}
			assertNoHandles(t, store)
		})
	}
}
