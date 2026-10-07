package sqlite

// Private staged tests: not part of the repository test suite until separately approved.
import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestStorageRetryImmutableMessageMismatchRollsBackStagedRevisionAndOperationAndAllowsSameID(t *testing.T) {
	registerCorruptionReadProbe(t)
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	entropy.fail.Store(true)

	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	failed, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
	requireFault(t, err, contracts.InvalidState)
	if failed.State != contracts.Failed || failed.Outcome != nil {
		t.Fatal("fixture must be a valid durable failed round")
	}
	request := rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "immutable-message-retry", Kind: "RetryRound", PublicFingerprint: [32]byte{7}}, CollectionID: failed.CollectionID, RoundID: failed.ID, Number: failed.Number, Attempt: failed.Attempt + 1, ExpectedRevision: failed.Revision, ExpectedVersion: failed.Version, Input: failed.Input, InitialState: contracts.Executing, AdmittedAt: storageNow}
	request.Input.Message = "different valid memo"
	if err := rl.ValidateRoundInput(request.Input); err != nil {
		t.Fatal("retry fixture must isolate immutable mismatch", err)
	}
	requestRaw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	// Out-of-transaction test observation counts actual SQL evaluation, while
	// every durable row (including sqlite_sequence) must still roll back.
	execSQL(t, store.db, `CREATE TRIGGER staged_retry_revision BEFORE UPDATE OF revision ON collections BEGIN SELECT jackpot_corruption_read_probe('revision'); END;
CREATE TRIGGER staged_retry_operation BEFORE INSERT ON operations WHEN NEW.operation_id='immutable-message-retry' BEGIN SELECT jackpot_corruption_read_probe('operation'); END`)
	before := corruptionDurableSnapshot(t, store)
	probe := new(corruptionReadProbe)
	t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
	for attempt := 0; attempt < 3; attempt++ {
		probe.calls.Store(0)
		activeCorruptionReadProbe.Store(probe)
		denied, err := store.AdmitRound(context.Background(), request)
		activeCorruptionReadProbe.Store(nil)
		requireFault(t, err, contracts.InvalidState)
		if !reflect.DeepEqual(denied, rl.Admission{}) || probe.calls.Load() != 2 {
			t.Fatal("mismatch did not traverse both staged writes before rollback", denied, probe.calls.Load())
		}
		if before != corruptionDurableSnapshot(t, store) {
			t.Fatal("staged retry leaked revision, operation, sequence or attempt")
		}
		operation, err := store.ReadOperation(context.Background(), request.Operation.ID)
		if err != nil || operation.Status != contracts.OperationUnknown {
			t.Fatal("rejected retry consumed operation ID", operation, err)
		}
		current, err := store.ReadRound(context.Background(), failed.ID)
		if err != nil || !reflect.DeepEqual(current, failed) {
			t.Fatal("retry mismatch changed original failed input or metadata", err)
		}
		currentRaw, err := json.Marshal(request)
		if err != nil || string(currentRaw) != string(requestRaw) || entropy.calls.Load() != 1 {
			t.Fatal("storage rejection mutated caller input or reached RNG", err)
		}
		assertNoHandles(t, store)
	}
	request.Input = failed.Input
	probe.calls.Store(0)
	activeCorruptionReadProbe.Store(probe)
	admitted, err := store.AdmitRound(context.Background(), request)
	activeCorruptionReadProbe.Store(nil)
	if err != nil || admitted.Replay || probe.calls.Load() != 2 || admitted.Round.State != contracts.Executing || admitted.Round.ID != failed.ID || admitted.Round.Attempt != failed.Attempt+1 || admitted.Round.Revision != failed.Revision+1 || admitted.Round.Version != failed.Version+1 || !reflect.DeepEqual(admitted.Round.Input, failed.Input) {
		t.Fatal("corrected same ID did not admit immutable retry exactly once", admitted, err)
	}
	durable := corruptionDurableSnapshot(t, store)
	replay, err := store.AdmitRound(context.Background(), request)
	if err != nil || !replay.Replay || !reflect.DeepEqual(replay.Round, admitted.Round) || durable != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
		t.Fatal("storage replay duplicated retry writes or entropy", err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}

func coreConditionAdmittedRound(t *testing.T, withClaim bool) (*Store, rl.RoundRecord, *rl.ClaimToken, *productEntropy) {
	t.Helper()
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.Close)
	admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	current := admission.Round
	var claim *rl.ClaimToken
	if withClaim {
		claim, err = store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: current.CollectionID, RoundID: current.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: current.Revision, ExpectedVersion: current.Version, Attempt: current.Attempt, Session: "session-one"})
		if err != nil || claim == nil {
			t.Fatal("fixture could not claim durable round", claim, err)
		}
		current, err = store.ReadRound(context.Background(), current.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current.State != contracts.Executing || current.Outcome != nil || entropy.calls.Load() != 0 {
		t.Fatal("fixture unexpectedly executed")
	}
	return store, current, claim, entropy
}

func TestStorageCommitInvalidPublicOutcomePrecedesLateResultDependencyFailureWithoutStateChange(t *testing.T) {
	for _, malformed := range []string{"algorithm", "ordered-slot"} {
		t.Run(malformed, func(t *testing.T) {
			store, round, claim, entropy := coreConditionAdmittedRound(t, true)
			valid := rl.Outcome{Winners: []rl.Winner{{ParticipantID: "p1", PrizeID: round.Input.Prizes[0].ID, Slot: 1}}, ExecutedAt: storageNow, AlgorithmVersion: "controlled-test", AppVersion: "core-port"}
			if err := rl.ValidateOutcome(round.Input, valid); err != nil {
				t.Fatal("control outcome invalid", err)
			}
			request := rl.CommitOutcomeRequest{Claim: *claim, ExpectedRevision: round.Revision, Outcome: valid}
			request.Outcome.Winners = append([]rl.Winner{}, valid.Winners...)
			if malformed == "algorithm" {
				request.Outcome.AlgorithmVersion = ""
			} else {
				request.Outcome.Winners[0].Slot = 2
			}
			requestRaw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, store.db, `CREATE TRIGGER core_commit_dependency_fault BEFORE INSERT ON results BEGIN SELECT RAISE(ABORT,'core late result dependency'); END`)
			before := corruptionDurableSnapshot(t, store)
			for attempt := 0; attempt < 3; attempt++ {
				result, err := store.CommitOutcome(context.Background(), request)
				requireFault(t, err, contracts.InvalidState)
				if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
					t.Fatal("invalid public outcome changed durable claim/result or exposed a winner")
				}
				current, err := store.ReadRound(context.Background(), round.ID)
				if err != nil || !reflect.DeepEqual(current, round) {
					t.Fatal("invalid outcome retired valid claim or pending operation", err)
				}
				raw, err := json.Marshal(request)
				if err != nil || string(raw) != string(requestRaw) {
					t.Fatal("validation changed caller outcome", err)
				}
				assertNoHandles(t, store)
			}
			// The same actual SQL fault is reached only after a valid outcome;
			// its dependency error must remain distinct from InvalidState.
			request.Outcome = valid
			result, err := store.CommitOutcome(context.Background(), request)
			var fault contracts.Fault
			if err == nil || errors.As(err, &fault) || !strings.Contains(err.Error(), "core late result dependency") || !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) {
				t.Fatal("valid outcome hid dependency error or leaked staged revision", err)
			}
			assertNoHandles(t, store)
			execSQL(t, store.db, "DROP TRIGGER core_commit_dependency_fault")
			completed, err := store.CommitOutcome(context.Background(), request)
			if err != nil || completed.State != contracts.Completed || completed.Revision != round.Revision+1 || completed.Version != round.Version+1 || !reflect.DeepEqual(completed.Outcome, &valid) || entropy.calls.Load() != 0 {
				t.Fatal("healthy same claim did not commit exactly once", err)
			}
			durable := corruptionDurableSnapshot(t, store)
			repeated, err := store.CommitOutcome(context.Background(), request)
			requireFault(t, err, contracts.StaleRevision)
			if !reflect.DeepEqual(repeated, rl.RoundRecord{}) || durable != corruptionDurableSnapshot(t, store) {
				t.Fatal("completed claim committed duplicate result")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestStorageAttemptFailureOperationAndOptionalClaimIndependentFencesPreserveExecutingOwner(t *testing.T) {
	for _, name := range []string{"foreign-operation-unclaimed", "foreign-operation-claimed", "provided-claim-without-stored-claim", "foreign-claim"} {
		t.Run(name, func(t *testing.T) {
			withClaim := name == "foreign-operation-claimed" || name == "foreign-claim"
			store, round, claim, entropy := coreConditionAdmittedRound(t, withClaim)
			operation := contracts.OperationID("create-op")
			request := rl.AttemptFailureRequest{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: operation, ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Attempt: round.Attempt, Claim: claim, Code: contracts.InvalidState, FailedAt: storageNow}
			expected := contracts.StaleRevision
			switch name {
			case "foreign-operation-unclaimed", "foreign-operation-claimed":
				request.OperationID = "unrelated-operation"
				expected = contracts.InvalidState
			case "provided-claim-without-stored-claim":
				request.Claim = &rl.ClaimToken{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: operation, Session: "session-one", Attempt: round.Attempt, Version: round.Version}
			case "foreign-claim":
				foreign := *claim
				foreign.Session = "different-owner"
				request.Claim = &foreign
			}
			rawBefore, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			before := corruptionDurableSnapshot(t, store)
			for attempt := 0; attempt < 3; attempt++ {
				result, err := store.RecordAttemptFailure(context.Background(), request)
				requireFault(t, err, expected)
				if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
					t.Fatal("foreign request retired or mutated live execution")
				}
				live, err := store.ReadRound(context.Background(), round.ID)
				if err != nil || !reflect.DeepEqual(live, round) {
					t.Fatal("foreign request changed claimed/unclaimed state", err)
				}
				op, err := store.ReadOperation(context.Background(), operation)
				if err != nil || op.Status != contracts.OperationPending {
					t.Fatal("foreign request completed original operation", op, err)
				}
				rawAfter, err := json.Marshal(request)
				if err != nil || string(rawAfter) != string(rawBefore) {
					t.Fatal("failure request mutated caller input", err)
				}
				assertNoHandles(t, store)
			}
			// Correct only the independently failing condition; no revision,
			// version, attempt or outcome precondition was changed above.
			request.OperationID, request.Claim = operation, claim
			failed, err := store.RecordAttemptFailure(context.Background(), request)
			if err != nil || failed.State != contracts.Failed || failed.Outcome != nil || failed.Revision != round.Revision+1 || failed.Version != round.Version+1 || failed.Attempt != round.Attempt || failed.FailureCode != contracts.InvalidState || !reflect.DeepEqual(failed.Claim, round.Claim) {
				t.Fatal("corrected owner could not record one durable failure", err)
			}
			op, err := store.ReadOperation(context.Background(), operation)
			if err != nil || op.Status != contracts.OperationFailed || op.FailureCode != contracts.InvalidState || op.Revision != failed.Revision {
				t.Fatal("valid failure disagrees with operation", op, err)
			}
			var failedAt string
			if err := store.db.QueryRow("SELECT failed_at FROM round_attempts WHERE round_id=? AND attempt=?", failed.ID, failed.Attempt).Scan(&failedAt); err != nil || failedAt != storageNow.UTC().Format(time.RFC3339Nano) {
				t.Fatal("valid failure did not record one attempt", err)
			}
			durable := corruptionDurableSnapshot(t, store)
			denied, err := store.RecordAttemptFailure(context.Background(), request)
			requireFault(t, err, contracts.StaleRevision)
			if !reflect.DeepEqual(denied, rl.RoundRecord{}) || durable != corruptionDurableSnapshot(t, store) {
				t.Fatal("terminal failure duplicated writes")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
func TestStorageCommitReadDependencyFailurePrecedesInvalidPublicOutcomeAtFirstNthAndContinuousReads(t *testing.T) {
	registerCorruptionReadProbe(t)
	for _, entry := range []struct {
		name, mode string
		at         int32
	}{{"first", "once", 1}, {"second", "once", 2}, {"continuous", "continuous", 1}} {
		t.Run(entry.name, func(t *testing.T) {
			store, round, claim, entropy := coreConditionAdmittedRound(t, true)
			request := rl.CommitOutcomeRequest{Claim: *claim, ExpectedRevision: round.Revision, Outcome: rl.Outcome{Winners: []rl.Winner{{ParticipantID: "p1", PrizeID: round.Input.Prizes[0].ID, Slot: 1}}, ExecutedAt: storageNow, AlgorithmVersion: "", AppVersion: "core-port"}}
			if err := rl.ValidateOutcome(round.Input, request.Outcome); err == nil {
				t.Fatal("fixture must isolate invalid public outcome")
			}
			rawBefore, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, store.db, `ALTER TABLE operations RENAME TO core_commit_operation_rows;
CREATE VIEW operations AS SELECT creation_key,operation_id,kind,collection_id,round_id,status,revision,jackpot_corruption_read_probe(public_fingerprint) AS public_fingerprint,failure_code FROM core_commit_operation_rows`)
			before := corruptionDurableSnapshot(t, store)
			t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
			probe := &corruptionReadProbe{at: entry.at, mode: entry.mode}
			activeCorruptionReadProbe.Store(probe)
			// Continuous failures share one probe; first/Nth failures reset for
			// each explicit caller retry. No sleep or elapsed-time oracle.
			for attempt := 0; attempt < 3; attempt++ {
				if entry.mode != "continuous" {
					probe.calls.Store(0)
				}
				result, err := store.CommitOutcome(context.Background(), request)
				activeCorruptionReadProbe.Store(nil)
				var fault contracts.Fault
				if err == nil || errors.As(err, &fault) || !strings.Contains(err.Error(), "injected actual SQLite read failure") || !reflect.DeepEqual(result, rl.RoundRecord{}) || probe.calls.Load() < entry.at {
					t.Fatal("public validation hid earlier dependency failure", err, probe.calls.Load())
				}
				if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
					t.Fatal("read fault changed claim, attempt, revision or results")
				}
				afterRaw, err := json.Marshal(request)
				if err != nil || string(afterRaw) != string(rawBefore) {
					t.Fatal("read fault changed caller input", err)
				}
				assertNoHandles(t, store)
				activeCorruptionReadProbe.Store(probe)
			}
			// public_fingerprint is not a WHERE operand: this scalar expression
			// is evaluated once per txOperation read, so second means the second
			// read (after full collection preflight), not predicate/projection
			// double evaluation inside the first read. Verify both healthy reads.
			probe.calls.Store(0)
			probe.at, probe.mode = 0, ""
			activeCorruptionReadProbe.Store(probe)
			result, err := store.CommitOutcome(context.Background(), request)
			activeCorruptionReadProbe.Store(nil)
			requireFault(t, err, contracts.InvalidState)
			if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || probe.calls.Load() != 2 {
				t.Fatal("after dependency recovery invalid outcome became admissible or SQL read observation was not one-per-read", probe.calls.Load())
			}
			assertNoHandles(t, store)
		})
	}
}
