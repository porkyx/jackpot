package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

// Compare logical durable bytes, not file/WAL layout: a read may checkpoint its
// WAL without changing application state. Every table is read in one snapshot.
func corruptionDurableSnapshot(t *testing.T, store *Store) string {
	t.Helper()
	tx, err := store.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	queries := []string{
		"SELECT * FROM collections ORDER BY id",
		"SELECT * FROM participants ORDER BY collection_id,position",
		"SELECT * FROM rounds ORDER BY collection_id,number",
		"SELECT * FROM operations ORDER BY creation_key",
		"SELECT * FROM round_candidates ORDER BY collection_id,round_id,position",
		"SELECT * FROM round_prizes ORDER BY collection_id,round_id,position",
		"SELECT * FROM round_attempts ORDER BY collection_id,round_id,attempt",
		"SELECT * FROM results ORDER BY collection_id,round_id",
		"SELECT * FROM winners ORDER BY collection_id,round_id,prize_id,slot",
		"SELECT * FROM sqlite_sequence ORDER BY name",
	}
	tables := make([][][]any, 0, len(queries))
	for _, query := range queries {
		rows, err := tx.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		table := make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i, v := range values {
				if blob, ok := v.([]byte); ok {
					values[i] = "blob:" + hex.EncodeToString(blob)
				}
			}
			table = append(table, values)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func requireCorruptionNoRound(t *testing.T, name string, round rl.RoundRecord, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(round, rl.RoundRecord{}) {
		t.Errorf("%s published corrupt round: %+v / %v", name, round, err)
	}
}
func TestRoundCorruptionCompletedInvariantStopsReadsRecoveryReplayAndEveryMutation(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"outcome algorithm missing", `UPDATE results SET outcome_json=json_set(outcome_json,'$.AlgorithmVersion','')`},
		{"outcome time missing", `UPDATE results SET outcome_json=json_set(outcome_json,'$.ExecutedAt','0001-01-01T00:00:00Z')`},
		{"outcome winner not candidate", `UPDATE results SET outcome_json=json_set(outcome_json,'$.Winners[0].ParticipantID','missing')`},
		{"outcome duplicate count", `UPDATE results SET outcome_json=json_set(outcome_json,'$.Winners',json('[]'))`},
		{"SQL winner differs", `DROP TRIGGER immutable_winner; UPDATE winners SET participant_id='p2'`},
		{"SQL winner extra slot", `DROP TRIGGER valid_winner_slot; INSERT INTO winners SELECT collection_id,round_id,'p2',prize_id,2 FROM winners`},
		{"SQL winner absent", `DROP TRIGGER undeletable_winner; DELETE FROM winners`},
		{"SQL winner slot differs", `DROP TRIGGER immutable_winner; UPDATE winners SET slot=2`},
		{"active operation pending", `UPDATE operations SET status='pending'`},
		{"active operation failed", `UPDATE operations SET status='failed',failure_code='InvalidState'`},
		{"active operation wrong identity", `PRAGMA foreign_keys=OFF; UPDATE operations SET collection_id='other'; PRAGMA foreign_keys=ON`},
		{"active operation wrong round", `PRAGMA foreign_keys=OFF; UPDATE operations SET round_id='other'; PRAGMA foreign_keys=ON`},
		{"result wrong collection owner", `PRAGMA foreign_keys=OFF; UPDATE results SET collection_id='other'; PRAGMA foreign_keys=ON`},
		{"active operation non-execution kind", `UPDATE operations SET kind='SetSchedule'`},
		{"operation future revision", `UPDATE operations SET revision=revision+1`},
		{"round state executing with result", `UPDATE rounds SET state='executing'`},
		{"round claim mismatch", `UPDATE rounds SET claim_json=json_set(claim_json,'$.Session','')`},
		{"immutable input invalid", `UPDATE rounds SET input_json=json_set(input_json,'$.Mode','unknown')`},
		{"result absent", `DROP TRIGGER undeletable_winner; DROP TRIGGER undeletable_result; DELETE FROM winners; DELETE FROM results`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := productRequest(storageNow, rl.ImmediateMode)
			completed, err := service.CreateCollection(context.Background(), request)
			if err != nil || completed.State != contracts.Completed {
				t.Fatal(completed, err)
			}
			corruptionMode(t, store)
			execSQL(t, store.db, test.sql)
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			round, err := store.ReadRound(context.Background(), completed.ID)
			requireCorruptionNoRound(t, "ReadRound", round, err)
			_, rounds, revision, err := store.ReadCollectionState(context.Background(), completed.CollectionID)
			if err == nil || rounds != nil || revision != 0 {
				t.Error("collection read exposed partial corrupt state", rounds, revision, err)
			}
			snapshot, snapshotRevision, err := store.ReadCollectionSnapshot(context.Background(), completed.CollectionID)
			if err == nil || snapshotRevision != 0 || !reflect.DeepEqual(snapshot, rl.FrozenCollection{}) {
				t.Error("snapshot query exposed corrupt history", snapshot, snapshotRevision, err)
			}
			page, total, err := store.ListCollectionPage(context.Background(), 0, 100, "", "all")
			if err == nil || page != nil || total != 0 {
				t.Error("history exposed corrupt state", page, total, err)
			}
			report, err := service.Recover(context.Background(), rl.RecoverRequest{})
			if err == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
				t.Error("Recover silently accepted or repaired corruption", report, err)
			}
			round, err = service.CreateCollection(context.Background(), request)
			requireCorruptionNoRound(t, "create replay", round, err)
			round, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: "blocked-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: completed.CollectionID, Revision: completed.Revision}, Prizes: []rl.Prize{{ID: "next", Count: 1}}, Mode: rl.ImmediateMode})
			requireCorruptionNoRound(t, "Rerun", round, err)
			roundContext := productRoundContext(completed, "session-one")
			delay := uint32(30)
			round, err = service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "blocked-schedule", Context: roundContext, QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
			requireCorruptionNoRound(t, "SetSchedule", round, err)
			round, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "blocked-cancel", Context: roundContext})
			requireCorruptionNoRound(t, "CancelSchedule", round, err)
			round, err = service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "blocked-retry", Context: roundContext})
			requireCorruptionNoRound(t, "RetryRound", round, err)
			round, err = service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: completed.ID})
			requireCorruptionNoRound(t, "ExecuteDue", round, err)
			if after := corruptionDurableSnapshot(t, store); after != before {
				t.Error("corrupt durable bytes were repaired or additional rows committed")
			}
			if entropy.calls.Load() != calls {
				t.Error("corruption consumed entropy")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestRoundCorruptionExecutingOperationAndClaimStopRecoveryWithoutRepair(t *testing.T) {
	for _, test := range []struct{ name, sql string }{
		{"pending operation succeeded", `UPDATE operations SET status='succeeded'`},
		{"pending operation failed", `UPDATE operations SET status='failed',failure_code='InvalidState'`},
		{"claim session absent", `UPDATE rounds SET claim_json=json_set(claim_json,'$.Session','')`},
		{"claim attempt differs", `UPDATE rounds SET claim_json=json_set(claim_json,'$.Attempt',2)`},
		{"claim operation differs", `UPDATE rounds SET claim_json=json_set(claim_json,'$.OperationID','different')`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := productRequest(storageNow, rl.ImmediateMode)
			prepared, err := service.PrepareCreate(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: admission.Round.CollectionID, RoundID: admission.Round.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: admission.Round.Revision, ExpectedVersion: admission.Round.Version, Attempt: admission.Round.Attempt, Session: "session-one"})
			if err != nil || claim == nil {
				t.Fatal(claim, err)
			}
			corruptionMode(t, store)
			execSQL(t, store.db, test.sql)
			before := corruptionDurableSnapshot(t, store)
			restarted := productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
			report, err := restarted.Recover(context.Background(), rl.RecoverRequest{})
			if err == nil || len(report.Failed)+len(report.Due)+len(report.Completed) != 0 {
				t.Error("corrupt executing state accepted or repaired", report, err)
			}
			if after := corruptionDurableSnapshot(t, store); after != before || entropy.calls.Load() != 0 {
				t.Error("recover modified corrupt state or consumed entropy")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestRoundCorruptionLaterCompletedDamagePreventsEarlierExecutingRecoveryMutation(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	firstRequest := productRequest(storageNow, rl.ImmediateMode)
	prepared, err := service.PrepareCreate(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.AdmitCreate(context.Background(), prepared, firstRequest.Collection)
	if err != nil {
		t.Fatal(err)
	}
	laterRequest := productRequest(storageNow, rl.ImmediateMode)
	laterRequest.OperationID = "later-create"
	laterRequest.Context.DraftID = "later-draft"
	laterRequest.Collection.SourceDraftID = "later-draft"
	later, err := service.CreateCollection(context.Background(), laterRequest)
	if err != nil {
		t.Fatal(err)
	}
	corruptionMode(t, store)
	execSQL(t, store.db, "UPDATE results SET outcome_json=json_set(outcome_json,'$.AppVersion','') WHERE round_id=?", later.ID)
	before := corruptionDurableSnapshot(t, store)
	restarted := productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
	report, err := restarted.Recover(context.Background(), rl.RecoverRequest{})
	if err == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
		t.Fatal("later damage did not stop entire preflight", report, err)
	}
	current, err := store.ReadRound(context.Background(), first.Round.ID)
	if err != nil || !reflect.DeepEqual(current, first.Round) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
		t.Fatal("earlier executing round changed before all stored rounds were validated", current, err)
	}
	assertNoHandles(t, store)
}

type corruptionReadProbe struct {
	calls  atomic.Int32
	at     int32
	mode   string
	cancel context.CancelFunc
}

var corruptionProbeRegistration sync.Once
var corruptionProbeRegistrationError error
var activeCorruptionReadProbe atomic.Pointer[corruptionReadProbe]

func registerCorruptionReadProbe(t *testing.T) {
	t.Helper()
	corruptionProbeRegistration.Do(func() {
		corruptionProbeRegistrationError = driverSQLite.RegisterScalarFunction("jackpot_corruption_read_probe", 1, func(_ *driverSQLite.FunctionContext, args []driver.Value) (driver.Value, error) {
			probe := activeCorruptionReadProbe.Load()
			if probe != nil {
				call := probe.calls.Add(1)
				if call == probe.at || (probe.mode == "continuous" && call >= probe.at) {
					if probe.mode == "cancel" {
						probe.cancel()
					} else {
						return nil, errors.New("injected actual SQLite read failure")
					}
				}
			}
			return args[0], nil
		})
	})
	if corruptionProbeRegistrationError != nil {
		t.Fatal(corruptionProbeRegistrationError)
	}
}
func TestRoundCorruptionActualReadFirstNthContinuousAndCancellationRollbackPreflight(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		at         int32
	}{
		{"first read", "once", 1}, {"Nth read after partial validation", "once", 8}, {"continuous read failure", "continuous", 1}, {"cancellation during partial validation", "cancel", 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			registerCorruptionReadProbe(t) // Driver functions are registered before Open.
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			for index := 0; index < 3; index++ {
				request := productRequest(storageNow, rl.ImmediateMode)
				request.OperationID = contracts.OperationID(fmt.Sprintf("completed-%d", index))
				request.Context.DraftID = contracts.DraftID(fmt.Sprintf("completed-draft-%d", index))
				request.Collection.SourceDraftID = request.Context.DraftID
				if _, err := service.CreateCollection(context.Background(), request); err != nil {
					t.Fatal(err)
				}
			}
			request := productRequest(storageNow, rl.ImmediateMode)
			prepared, err := service.PrepareCreate(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
			if err != nil {
				t.Fatal(err)
			}
			// This test-only view places a real driver error at SQLite's read boundary.
			// It does not replace a lifecycle return value or fabricate a product result.
			execSQL(t, store.db, `ALTER TABLE rounds RENAME TO corruption_round_rows;
    CREATE VIEW rounds AS SELECT collection_id,jackpot_corruption_read_probe(id) AS id,number,state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM corruption_round_rows`)
			before := corruptionDurableSnapshot(t, store)
			restarted := productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &corruptionReadProbe{at: test.at, mode: test.mode, cancel: cancel}
			activeCorruptionReadProbe.Store(probe)
			defer activeCorruptionReadProbe.Store(nil)
			report, err := restarted.Recover(ctx, rl.RecoverRequest{})
			activeCorruptionReadProbe.Store(nil)
			if err == nil || probe.calls.Load() < test.at || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
				t.Fatal("read fault accepted partial recovery", report, err, probe.calls.Load())
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
				t.Fatal("read fault changed durable rows or entropy")
			}
			assertNoHandles(t, store)
			// Re-read the exact unfinished record after rollback. A healthy retry checks
			// all completed records and retires this prior-session admission once.
			current, err := store.ReadRound(context.Background(), admission.Round.ID)
			if err != nil || !reflect.DeepEqual(current, admission.Round) {
				t.Fatal(current, err)
			}
			// Restore the real table for a recovery write; ALTER never changes row bytes.
			execSQL(t, store.db, "DROP VIEW rounds; ALTER TABLE corruption_round_rows RENAME TO rounds")
			report, err = restarted.Recover(context.Background(), rl.RecoverRequest{})
			if err != nil || len(report.Failed) != 1 || report.Failed[0] != admission.Round.ID || entropy.calls.Load() != 3 {
				t.Fatal("healthy preflight retry", report, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestRoundCorruptionEarlierResultBlocksEveryOtherwiseValidStorageWrite(t *testing.T) {
	for _, command := range []string{"admission", "claim", "commit", "failure"} {
		t.Run(command, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			first, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
			if err != nil {
				t.Fatal(err)
			}
			input := rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p2", "p3"}, Prizes: []rl.Prize{{ID: "next-prize", Name: "다음", Count: 1}}, Mode: rl.ImmediateMode}
			admission, err := store.AdmitRound(context.Background(), rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "second-admission", Kind: "Rerun"}, CollectionID: first.CollectionID, RoundID: "second-round", ExpectedRevision: first.Revision, Number: 2, Attempt: 1, Input: input, InitialState: contracts.Executing, AdmittedAt: storageNow})
			if err != nil {
				t.Fatal(err)
			}
			current := admission.Round
			var claim *rl.ClaimToken
			if command == "commit" || command == "failure" || command == "admission" {
				claim, err = store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: current.CollectionID, RoundID: current.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: current.Revision, ExpectedVersion: current.Version, Attempt: current.Attempt, Session: "session-one"})
				if err != nil || claim == nil {
					t.Fatal(claim, err)
				}
				current, err = store.ReadRound(context.Background(), current.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			validOutcome := rl.Outcome{Winners: []rl.Winner{{ParticipantID: "p2", PrizeID: "next-prize", Slot: 1}}, ExecutedAt: storageNow, AlgorithmVersion: "test-fixed", AppVersion: "corruption-test"}
			if command == "admission" {
				current, err = store.CommitOutcome(context.Background(), rl.CommitOutcomeRequest{Claim: *claim, ExpectedRevision: current.Revision, Outcome: validOutcome})
				if err != nil {
					t.Fatal(err)
				}
			}
			execSQL(t, store.db, "DROP TRIGGER immutable_result; UPDATE results SET outcome_json=json_set(outcome_json,'$.AppVersion','') WHERE round_id=?", first.ID)
			before := corruptionDurableSnapshot(t, store)
			latest, err := store.ReadRound(context.Background(), current.ID)
			if err != nil || !reflect.DeepEqual(latest, current) {
				t.Fatal("latest fixture must be independently valid", latest, err)
			}
			switch command {
			case "admission":
				denied, err := store.AdmitRound(context.Background(), rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "blocked-third", Kind: "Rerun"}, CollectionID: current.CollectionID, RoundID: "third-round", ExpectedRevision: current.Revision, Number: 3, Attempt: 1, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p3"}, Prizes: []rl.Prize{{ID: "last", Count: 1}}, Mode: rl.ImmediateMode}, InitialState: contracts.Executing, AdmittedAt: storageNow})
				if err == nil || !reflect.DeepEqual(denied, rl.Admission{}) {
					t.Fatal("admission hid corrupt past round", denied, err)
				}
			case "claim":
				denied, err := store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: current.CollectionID, RoundID: current.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: current.Revision, ExpectedVersion: current.Version, Attempt: current.Attempt, Session: "session-one"})
				if err == nil || denied != nil {
					t.Fatal("claim hid corrupt past round", denied, err)
				}
			case "commit":
				denied, err := store.CommitOutcome(context.Background(), rl.CommitOutcomeRequest{Claim: *claim, ExpectedRevision: current.Revision, Outcome: validOutcome})
				requireCorruptionNoRound(t, "commit with corrupt past", denied, err)
			case "failure":
				denied, err := store.RecordAttemptFailure(context.Background(), rl.AttemptFailureRequest{CollectionID: current.CollectionID, RoundID: current.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: current.Revision, ExpectedVersion: current.Version, Attempt: current.Attempt, Claim: claim, Code: contracts.InvalidState, FailedAt: storageNow})
				requireCorruptionNoRound(t, "failure repair with corrupt past", denied, err)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
				t.Fatal("write changed damaged collection or entropy")
			}
			assertNoHandles(t, store)
		})
	}
}
func TestRoundCorruptionEarlierCancelledOperationBlocksOtherwiseValidScheduleAndCancel(t *testing.T) {
	for _, command := range []string{"schedule", "cancel"} {
		t.Run(command, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			first, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			cancelled, err := service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "first-cancel", Context: productRoundContext(first, "session-one")})
			if err != nil {
				t.Fatal(err)
			}
			current, err := service.Rerun(context.Background(), rl.RerunRequest{OperationID: "second-reservation", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: cancelled.Revision}, Prizes: []rl.Prize{{ID: "second-prize", Count: 1}}, Mode: rl.ReservationMode})
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, store.db, "DROP TRIGGER immutable_terminal_operation; UPDATE operations SET status='pending' WHERE operation_id='create-op'")
			before := corruptionDurableSnapshot(t, store)
			delay := uint32(30)
			if command == "schedule" {
				denied, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "blocked-latest-schedule", Context: productRoundContext(current, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
				requireCorruptionNoRound(t, command, denied, err)
			} else {
				denied, err := service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "blocked-latest-cancel", Context: productRoundContext(current, "session-one")})
				requireCorruptionNoRound(t, command, denied, err)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("schedule/cancel ignored earlier corruption")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestRoundCorruptionFailedOperationCodeMismatchBlocksRetryAndRecovery(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	entropy.fail.Store(true)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	failed, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
	if err == nil || failed.State != contracts.Failed {
		t.Fatal(failed, err)
	}
	corruptionMode(t, store)
	execSQL(t, store.db, "UPDATE operations SET failure_code='InvalidInput'")
	before := corruptionDurableSnapshot(t, store)
	entropy.fail.Store(false)
	round, err := store.ReadRound(context.Background(), failed.ID)
	requireCorruptionNoRound(t, "failed round operation code", round, err)
	round, err = service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "blocked-failed-retry", Context: productRoundContext(failed, "session-one")})
	requireCorruptionNoRound(t, "failed retry", round, err)
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
		t.Fatal("failed corruption hidden", report, err)
	}
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
		t.Fatal("failed corruption repaired or consumed entropy")
	}
	assertNoHandles(t, store)
}
