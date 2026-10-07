package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestStorageDirectAdmissionReplayConflictsPreserveTheRecordedOperation(t *testing.T) {
	for _, mismatch := range []string{"kind", "fingerprint"} {
		t.Run(mismatch, func(t *testing.T) {
			store, entropy, healthy := directStorageAdmitFixture(t, "CreateCollection", rl.ImmediateMode)
			first, err := store.AdmitRound(context.Background(), healthy)
			if err != nil || first.Replay {
				t.Fatal(first, err)
			}
			before := corruptionDurableSnapshot(t, store)
			bad := directStorageAdmitCopy(t, healthy)
			if mismatch == "kind" {
				bad.Operation.Kind, bad.NewCollection = "Rerun", nil
			} else {
				bad.Operation.PublicFingerprint[0]++
			}
			expected := directStorageAdmitCopy(t, bad)
			for repeat := 0; repeat < 2; repeat++ {
				got, err := store.AdmitRound(context.Background(), bad)
				if !errors.Is(err, rl.ErrOperationConflict) || !reflect.DeepEqual(got, rl.Admission{}) ||
					before != corruptionDurableSnapshot(t, store) || !reflect.DeepEqual(bad, expected) || entropy.calls.Load() != 0 {
					t.Fatal("direct replay conflict changed durable state or caller input", got, err)
				}
				assertNoHandles(t, store)
			}
			replay, err := store.AdmitRound(context.Background(), healthy)
			if err != nil || !replay.Replay || !reflect.DeepEqual(replay.Round, first.Round) ||
				!reflect.DeepEqual(replay.Operation, first.Operation) || before != corruptionDurableSnapshot(t, store) {
				t.Fatal("conflict damaged the original exact replay", replay, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestStorageDirectCreateDuplicateSourcePreservesItsOriginalOwnerAndReusableOperation(t *testing.T) {
	store, entropy, original := directStorageAdmitFixture(t, "CreateCollection", rl.ImmediateMode)
	first, err := store.AdmitRound(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	healthy := directStorageAdmitCopy(t, original)
	healthy.Operation.ID, healthy.CollectionID, healthy.RoundID = "second-create", "second-collection", "second-round"
	healthy.NewCollection.ID, healthy.NewCollection.SourceDraftID = healthy.CollectionID, "second-draft"
	bad := directStorageAdmitCopy(t, healthy)
	bad.NewCollection.SourceDraftID = original.NewCollection.SourceDraftID
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 2; repeat++ {
		got, err := store.AdmitRound(context.Background(), bad)
		var finalized *rl.FinalizedError
		if !errors.As(err, &finalized) || finalized.CollectionID != first.Round.CollectionID ||
			!reflect.DeepEqual(got, rl.Admission{}) || before != corruptionDurableSnapshot(t, store) {
			t.Fatal("duplicate source did not preserve its original owner", got, err)
		}
		op, err := store.ReadOperation(context.Background(), healthy.Operation.ID)
		if err != nil || op.Status != contracts.OperationUnknown {
			t.Fatal("duplicate source consumed the new operation", op, err)
		}
		assertNoHandles(t, store)
	}
	directStorageAdmitHealthyReplay(t, store, entropy, healthy, 0)
}

func TestStorageDirectRetryAndDueEachStatefulGuardRollsBackStagedWrites(t *testing.T) {
	cases := []struct {
		name, kind string
		code       contracts.ErrorCode
		mutate     func(*testing.T, *Store, *rl.AdmitRoundRequest)
	}{
		{"retry foreign collection", "RetryRound", contracts.StaleRevision, func(t *testing.T, store *Store, r *rl.AdmitRoundRequest) {
			input := productRequest(storageNow, rl.ImmediateMode)
			frozen := input.Collection
			frozen.ID, frozen.SourceDraftID = "other-collection", "other-draft"
			other, err := store.AdmitRound(context.Background(), rl.AdmitRoundRequest{
				Operation:    rl.OperationIdentity{ID: "other-create", Kind: "CreateCollection", PublicFingerprint: [32]byte{31}},
				CollectionID: frozen.ID, NewCollection: &frozen, ExpectedRevision: frozen.FinalizedDraftRevision,
				RoundID: "other-round", Number: 1, Attempt: 1, Input: input.Input, InitialState: contracts.Executing, AdmittedAt: storageNow,
			})
			if err != nil {
				t.Fatal(err)
			}
			r.CollectionID, r.ExpectedRevision = other.Round.CollectionID, other.Round.Revision
		}},
		{"retry version mismatch", "RetryRound", contracts.StaleRevision, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.ExpectedVersion++ }},
		{"retry attempt mismatch", "RetryRound", contracts.InvalidState, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.Attempt++ }},
		{"due version mismatch", "ExecuteDue", contracts.StaleRevision, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.ExpectedVersion++ }},
		{"due before exact deadline", "ExecuteDue", contracts.InvalidState, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) {
			r.AdmittedAt = r.AdmittedAt.Add(-time.Nanosecond)
		}},
		{"due attempt mismatch", "ExecuteDue", contracts.InvalidState, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.Attempt++ }},
		{"due empty session", "ExecuteDue", contracts.InvalidInput, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.Session = "" }},
		{"scheduled cannot retry", "ExecuteDue", contracts.InvalidState, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.Operation.Kind = "RetryRound"; r.Attempt++ }},
		{"failed reservation cannot execute due", "FailedDue", contracts.InvalidState, func(_ *testing.T, _ *Store, r *rl.AdmitRoundRequest) { r.Operation.Kind = "ExecuteDue"; r.Attempt-- }},
		{"completed outcome cannot retry", "Rerun", contracts.StaleRevision, func(t *testing.T, store *Store, r *rl.AdmitRoundRequest) {
			_, rounds, _, err := store.ReadCollectionState(context.Background(), r.CollectionID)
			if err != nil || len(rounds) != 1 || rounds[0].Outcome == nil {
				t.Fatal("completed fixture required", rounds, err)
			}
			before := rounds[0]
			r.Operation.Kind, r.RoundID, r.Number, r.Attempt = "RetryRound", before.ID, before.Number, before.Attempt+1
			r.ExpectedVersion, r.Input = before.Version, before.Input
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode := rl.ImmediateMode
			if tc.kind == "ExecuteDue" {
				mode = rl.ReservationMode
			}
			var store *Store
			var entropy *productEntropy
			var healthy rl.AdmitRoundRequest
			if tc.kind == "FailedDue" {
				store, entropy, healthy = directStorageFailedReservationRetryFixture(t)
			} else {
				store, entropy, healthy = directStorageAdmitFixture(t, tc.kind, mode)
			}
			bad := directStorageAdmitCopy(t, healthy)
			tc.mutate(t, store, &bad)
			execSQL(t, store.db, `CREATE TRIGGER direct_stateful_revision BEFORE UPDATE OF revision ON collections BEGIN SELECT jackpot_corruption_read_probe('revision'); END;
CREATE TRIGGER direct_stateful_operation BEFORE INSERT ON operations BEGIN SELECT jackpot_corruption_read_probe('operation'); END`)
			before := corruptionDurableSnapshot(t, store)
			expected := directStorageAdmitCopy(t, bad)
			calls := entropy.calls.Load()
			probe := new(corruptionReadProbe)
			t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
			for repeat := 0; repeat < 2; repeat++ {
				probe.calls.Store(0)
				activeCorruptionReadProbe.Store(probe)
				got, err := store.AdmitRound(context.Background(), bad)
				activeCorruptionReadProbe.Store(nil)
				requireFault(t, err, tc.code)
				if !reflect.DeepEqual(got, rl.Admission{}) || probe.calls.Load() != 2 || before != corruptionDurableSnapshot(t, store) ||
					!reflect.DeepEqual(bad, expected) || entropy.calls.Load() != calls {
					t.Fatal("stateful guard failed to roll back both staged writes", got, probe.calls.Load(), err)
				}
				op, err := store.ReadOperation(context.Background(), healthy.Operation.ID)
				if err != nil || op.Status != contracts.OperationUnknown {
					t.Fatal("stateful rejection consumed operation ID", op, err)
				}
				assertNoHandles(t, store)
			}
			directStorageAdmitHealthyReplay(t, store, entropy, healthy, calls)
		})
	}
}

func directStorageFailedReservationRetryFixture(t *testing.T) (*Store, *productEntropy, rl.AdmitRoundRequest) {
	t.Helper()
	store, entropy, scheduled := directStorageAdmitFixture(t, "ExecuteDue", rl.ReservationMode)
	entropy.fail.Store(true)
	service := productService(t, store, entropy, func() time.Time { return scheduled.AdmittedAt }, "session-one", nil)
	failed, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.RoundID})
	requireFault(t, err, contracts.InvalidState)
	if failed.State != contracts.Failed || failed.Outcome != nil || failed.ScheduledAt == nil || entropy.calls.Load() != 1 {
		t.Fatal("actual failed reservation must retain its scheduled deadline", failed, err)
	}
	operation := scheduled.Operation
	operation.Kind = "RetryRound"
	return store, entropy, rl.AdmitRoundRequest{
		Operation: operation, CollectionID: failed.CollectionID, ExpectedRevision: failed.Revision, ExpectedVersion: failed.Version,
		RoundID: failed.ID, Number: failed.Number, Attempt: failed.Attempt + 1, Input: failed.Input,
		InitialState: contracts.Executing, AdmittedAt: scheduled.AdmittedAt, Session: scheduled.Session,
	}
}

func TestStorageDirectRetryVersionMaximumRollsBackAndOneBelowCanAdmit(t *testing.T) {
	store, entropy, request := directStorageAdmitFixture(t, "RetryRound", rl.ImmediateMode)
	execSQL(t, store.db, "UPDATE rounds SET version=? WHERE id=?", contracts.MaxSafeInteger, request.RoundID)
	request.ExpectedVersion = contracts.RoundVersion(contracts.MaxSafeInteger)
	if _, err := store.ReadRound(context.Background(), request.RoundID); err != nil {
		t.Fatal("maximum counter must be a valid stored round", err)
	}
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 2; repeat++ {
		got, err := store.AdmitRound(context.Background(), request)
		requireFault(t, err, contracts.InvalidInput)
		if !reflect.DeepEqual(got, rl.Admission{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 {
			t.Fatal("version overflow leaked a staged revision or operation", got, err)
		}
		op, err := store.ReadOperation(context.Background(), request.Operation.ID)
		if err != nil || op.Status != contracts.OperationUnknown {
			t.Fatal("version overflow consumed the reusable operation", op, err)
		}
		assertNoHandles(t, store)
	}
	// Counter seeding exercises representability, not quadrillions of executions.
	execSQL(t, store.db, "UPDATE rounds SET version=? WHERE id=?", contracts.MaxSafeInteger-1, request.RoundID)
	request.ExpectedVersion--
	directStorageAdmitHealthyReplay(t, store, entropy, request, 1)
}

func directStorageAdmitHealthyReplay(t *testing.T, store *Store, entropy *productEntropy, request rl.AdmitRoundRequest, calls int32) {
	t.Helper()
	expected := directStorageAdmitCopy(t, request)
	first, err := store.AdmitRound(context.Background(), request)
	if err != nil || first.Replay || first.Round.ID != request.RoundID || first.Round.CollectionID != request.CollectionID ||
		first.Round.State != request.InitialState || first.Round.Attempt != request.Attempt || first.Round.Number != request.Number ||
		first.Operation.Operation != request.Operation || !reflect.DeepEqual(first.Round.Input, request.Input) {
		t.Fatal("corrected admission did not preserve exact input", first, err)
	}
	expectedRevision, expectedVersion := request.ExpectedRevision+1, contracts.RoundVersion(1)
	if request.NewCollection != nil {
		expectedRevision = 1
	}
	if request.Operation.Kind == "RetryRound" || request.Operation.Kind == "ExecuteDue" {
		expectedVersion = request.ExpectedVersion + 1
	}
	if first.Round.Revision != expectedRevision || first.Operation.Revision != expectedRevision || first.Round.Version != expectedVersion {
		t.Fatal("corrected admission advanced counters incorrectly", first)
	}
	after := corruptionDurableSnapshot(t, store)
	replay, err := store.AdmitRound(context.Background(), request)
	if err != nil || !replay.Replay || !reflect.DeepEqual(replay.Round, first.Round) || !reflect.DeepEqual(replay.Operation, first.Operation) ||
		after != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls || !reflect.DeepEqual(request, expected) {
		t.Fatal("corrected exact replay changed state, input or entropy", replay, err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
