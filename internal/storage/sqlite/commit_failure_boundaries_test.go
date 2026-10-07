package sqlite

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func commitFailureFirstWriteProbe(t *testing.T, store *Store) *corruptionReadProbe {
	t.Helper()
	registerCorruptionReadProbe(t)
	execSQL(t, store.db, "CREATE TRIGGER commit_failure_first_write BEFORE UPDATE OF revision ON collections BEGIN SELECT jackpot_corruption_read_probe('first-write'); END")
	probe := new(corruptionReadProbe)
	t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
	return probe
}

func commitFailureOwnerUnchanged(t *testing.T, store *Store, entropy *productEntropy, before string, round rl.RoundRecord, operation rl.OperationRecord) {
	t.Helper()
	assertNoHandles(t, store)
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
		t.Fatal("rejection changed durable tables/sequence or consumed entropy")
	}
	current, err := store.ReadRound(context.Background(), round.ID)
	if err != nil || !reflect.DeepEqual(current, round) {
		t.Fatal("rejection changed the executing claim, immutable input or round", err)
	}
	observed, err := store.ReadOperation(context.Background(), operation.Operation.ID)
	if err != nil || !reflect.DeepEqual(observed, operation) {
		t.Fatal("rejection changed its pending operation", err)
	}
	assertNoHandles(t, store)
}

func commitFailureCloseAndReleaseFile(t *testing.T, store *Store) {
	t.Helper()
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
	if err := store.Close(); err != nil {
		t.Fatal("close private fixture", err)
	}
	if store.db.Stats().OpenConnections != 0 {
		t.Fatal("closed fixture retained SQL connections")
	}
	// This is the existing t.TempDir database, never user data. Its handle must
	// be released before the operating system permits a rename.
	if err := os.Rename(store.path, store.path+".released"); err != nil {
		t.Fatal("closed fixture retained a database file handle", err)
	}
}

func TestStorageCommitForeignClaimSessionRejectsBeforeWritesAndCorrectedOwnerCommits(t *testing.T) {
	registerCorruptionReadProbe(t)
	store, round, claim, entropy := coreConditionAdmittedRound(t, true)
	operation, err := store.ReadOperation(context.Background(), claim.OperationID)
	if err != nil || operation.Status != contracts.OperationPending {
		t.Fatal("fixture must have one pending operation", operation, err)
	}
	outcome := rl.Outcome{Winners: []rl.Winner{{ParticipantID: "p1", PrizeID: round.Input.Prizes[0].ID, Slot: 1}}, ExecutedAt: storageNow, AlgorithmVersion: "controlled-test", AppVersion: "claim-boundary"}
	if err := rl.ValidateOutcome(round.Input, outcome); err != nil {
		t.Fatal("healthy outcome must pass independently", err)
	}
	request := rl.CommitOutcomeRequest{Claim: *claim, ExpectedRevision: round.Revision, Outcome: outcome}
	request.Claim.Session = "different-owner"
	if round.Claim == nil || *round.Claim == request.Claim || round.State != contracts.Executing || round.Version != request.Claim.Version || round.Revision != request.ExpectedRevision {
		t.Fatal("foreign Session must isolate only claim equality")
	}
	expectedRequest := jsonValue(t, request)
	probe := commitFailureFirstWriteProbe(t, store)
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 2; repeat++ {
		probe.calls.Store(0)
		activeCorruptionReadProbe.Store(probe)
		denied, err := store.CommitOutcome(context.Background(), request)
		activeCorruptionReadProbe.Store(nil)
		requireFault(t, err, contracts.StaleRevision)
		if !reflect.DeepEqual(denied, rl.RoundRecord{}) || probe.calls.Load() != 0 || jsonValue(t, request) != expectedRequest {
			t.Fatal("foreign claim returned state, evaluated a write or mutated caller input", denied, probe.calls.Load())
		}
		commitFailureOwnerUnchanged(t, store, entropy, before, round, operation)
	}
	// Correct only the owner Session; version/revision/input/outcome remain exact.
	request.Claim.Session = claim.Session
	expectedRequest = jsonValue(t, request)
	probe.calls.Store(0)
	activeCorruptionReadProbe.Store(probe)
	completed, err := store.CommitOutcome(context.Background(), request)
	activeCorruptionReadProbe.Store(nil)
	if err != nil || probe.calls.Load() != 1 || completed.State != contracts.Completed || completed.Revision != round.Revision+1 || completed.Version != round.Version+1 ||
		completed.Attempt != round.Attempt || !reflect.DeepEqual(completed.Input, round.Input) || !reflect.DeepEqual(completed.Claim, claim) || !reflect.DeepEqual(completed.Outcome, &outcome) {
		t.Fatal("corrected Session did not commit exact outcome once", completed, err, probe.calls.Load())
	}
	op, err := store.ReadOperation(context.Background(), claim.OperationID)
	if err != nil || op.Status != contracts.OperationSucceeded || op.Revision != completed.Revision || op.FailureCode != "" || op.Operation != operation.Operation {
		t.Fatal("completed operation disagrees with its result", op, err)
	}
	var results, winners int
	if err := store.db.QueryRow("SELECT count(*) FROM results").Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM winners").Scan(&winners); err != nil || results != 1 || winners != 1 {
		t.Fatal("corrected claim did not create exactly one result and winner", results, winners, err)
	}
	assertNoHandles(t, store)
	durable := corruptionDurableSnapshot(t, store)
	probe.calls.Store(0)
	activeCorruptionReadProbe.Store(probe)
	duplicate, err := store.CommitOutcome(context.Background(), request)
	activeCorruptionReadProbe.Store(nil)
	requireFault(t, err, contracts.StaleRevision)
	if !reflect.DeepEqual(duplicate, rl.RoundRecord{}) || probe.calls.Load() != 0 || durable != corruptionDurableSnapshot(t, store) ||
		entropy.calls.Load() != 0 || jsonValue(t, request) != expectedRequest {
		t.Fatal("duplicate commit changed caller/durable state or executed again", duplicate, probe.calls.Load())
	}
	commitFailureCloseAndReleaseFile(t, store)
}

func TestStorageFailureInvalidCodeRejectsBeforeWritesAndCorrectedCodeRetiresOwner(t *testing.T) {
	commitFailureRejectAndCorrect(t, "unknown-failure-code", storageNow)
}

func TestStorageFailureZeroTimestampRejectsBeforeWritesAndCorrectedTimePersists(t *testing.T) {
	commitFailureRejectAndCorrect(t, contracts.InvalidState, time.Time{})
}

func commitFailureRejectAndCorrect(t *testing.T, code contracts.ErrorCode, failedAt time.Time) {
	t.Helper()
	registerCorruptionReadProbe(t)
	store, round, claim, entropy := coreConditionAdmittedRound(t, true)
	operation, err := store.ReadOperation(context.Background(), claim.OperationID)
	if err != nil || operation.Status != contracts.OperationPending {
		t.Fatal("fixture must have one pending operation", operation, err)
	}
	request := rl.AttemptFailureRequest{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: claim.OperationID,
		ExpectedVersion: round.Version, ExpectedRevision: round.Revision, Attempt: round.Attempt, Claim: claim, Code: code, FailedAt: failedAt}
	if (request.Code.Validate() != nil) == request.FailedAt.IsZero() {
		t.Fatal("exactly one failure metadata operand must reject")
	}
	expectedRequest := jsonValue(t, request)
	probe := commitFailureFirstWriteProbe(t, store)
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 2; repeat++ {
		probe.calls.Store(0)
		activeCorruptionReadProbe.Store(probe)
		denied, err := store.RecordAttemptFailure(context.Background(), request)
		activeCorruptionReadProbe.Store(nil)
		requireFault(t, err, contracts.InvalidInput)
		if !reflect.DeepEqual(denied, rl.RoundRecord{}) || probe.calls.Load() != 0 || jsonValue(t, request) != expectedRequest {
			t.Fatal("bad failure metadata returned state, evaluated a write or mutated caller", denied, probe.calls.Load())
		}
		commitFailureOwnerUnchanged(t, store, entropy, before, round, operation)
	}
	if request.Code.Validate() != nil {
		request.Code = contracts.InvalidState
	} else {
		request.FailedAt = storageNow
	}
	expectedRequest = jsonValue(t, request)
	probe.calls.Store(0)
	activeCorruptionReadProbe.Store(probe)
	failed, err := store.RecordAttemptFailure(context.Background(), request)
	activeCorruptionReadProbe.Store(nil)
	if err != nil || probe.calls.Load() != 1 || failed.State != contracts.Failed || failed.Outcome != nil || failed.Revision != round.Revision+1 ||
		failed.Version != round.Version+1 || failed.Attempt != round.Attempt || failed.FailureCode != contracts.InvalidState ||
		!reflect.DeepEqual(failed.Claim, round.Claim) || !reflect.DeepEqual(failed.Input, round.Input) {
		t.Fatal("corrected single metadata value did not retire exactly one owner", failed, err, probe.calls.Load())
	}
	op, err := store.ReadOperation(context.Background(), claim.OperationID)
	if err != nil || op.Status != contracts.OperationFailed || op.Revision != failed.Revision || op.FailureCode != contracts.InvalidState || op.Operation != operation.Operation {
		t.Fatal("failed operation disagrees with durable round", op, err)
	}
	var attempts, results, winners int
	var actualCode, actualTime string
	if err := store.db.QueryRow("SELECT count(*),failure_code,failed_at FROM round_attempts").Scan(&attempts, &actualCode, &actualTime); err != nil ||
		attempts != 1 || actualCode != string(contracts.InvalidState) || actualTime != storageNow.UTC().Format(time.RFC3339Nano) {
		t.Fatal("failure attempt/time not exact and singular", attempts, actualCode, actualTime, err)
	}
	if err := store.db.QueryRow("SELECT (SELECT count(*) FROM results),(SELECT count(*) FROM winners)").Scan(&results, &winners); err != nil || results != 0 || winners != 0 {
		t.Fatal("failure fabricated an outcome", results, winners, err)
	}
	assertNoHandles(t, store)
	durable := corruptionDurableSnapshot(t, store)
	probe.calls.Store(0)
	activeCorruptionReadProbe.Store(probe)
	duplicate, err := store.RecordAttemptFailure(context.Background(), request)
	activeCorruptionReadProbe.Store(nil)
	requireFault(t, err, contracts.StaleRevision)
	if !reflect.DeepEqual(duplicate, rl.RoundRecord{}) || probe.calls.Load() != 0 || durable != corruptionDurableSnapshot(t, store) ||
		entropy.calls.Load() != 0 || jsonValue(t, request) != expectedRequest {
		t.Fatal("duplicate failure changed caller/durable state or reached entropy", duplicate, probe.calls.Load())
	}
	commitFailureCloseAndReleaseFile(t, store)
}
