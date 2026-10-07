package sqlite

// Direct typed admission boundary regressions use the existing tiny product fixture.
// They preserve API errors, write-free rejection and exactly-once correction/replay.
import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestStorageAdmitRoundTypedEntryRejectsEachBadValueBeforeWritesAndPreservesOperation(t *testing.T) {
	cases := []struct {
		name, kind string
		mutate     func(*rl.AdmitRoundRequest)
	}{
		{"empty operation", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.Operation.ID = "" }},
		{"empty collection", "Rerun", func(r *rl.AdmitRoundRequest) { r.CollectionID = "" }},
		{"empty round", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.RoundID = "" }},
		{"zero number", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.Number = 0 }},
		{"zero attempt", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.Attempt = 0 }},
		{"zero admitted time", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.AdmittedAt = time.Time{} }},
		{"scheduled initial state", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.InitialState = contracts.Scheduled }},
		{"nonzero year zero", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.AdmittedAt = time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC) }},
		{"year ten thousand", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.AdmittedAt = time.Date(10000, 1, 2, 0, 0, 0, 0, time.UTC) }},
		{"unknown operation kind", "Rerun", func(r *rl.AdmitRoundRequest) { r.Operation.Kind = "UnknownAdmission" }},
		{"create without frozen collection", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.NewCollection = nil }},
		{"invalid memo validator dispatch", "CreateCollection", func(r *rl.AdmitRoundRequest) { r.Input.Message = strings.Repeat("a", 21) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, entropy, healthy := directStorageAdmitFixture(t, tc.kind, rl.ImmediateMode)
			bad := directStorageAdmitCopy(t, healthy)
			tc.mutate(&bad)
			directStorageAdmitRejectAndCorrect(t, store, entropy, healthy, bad)
		})
	}
}

func TestStorageAdmitRoundKindSnapshotAndInitialStatePairsRejectWithoutConsumingOperation(t *testing.T) {
	cases := []struct {
		name, kind, mode string
		mutate           func(*rl.AdmitRoundRequest)
	}{
		{"immediate create cannot start pending", "CreateCollection", rl.ImmediateMode, func(r *rl.AdmitRoundRequest) { r.InitialState = contracts.PendingSchedule }},
		{"reservation create cannot start executing", "CreateCollection", rl.ReservationMode, func(r *rl.AdmitRoundRequest) { r.InitialState = contracts.Executing }},
		{"rerun cannot introduce frozen collection", "Rerun", rl.ImmediateMode, func(r *rl.AdmitRoundRequest) {
			frozen := productRequest(storageNow, rl.ImmediateMode).Collection
			frozen.ID = r.CollectionID
			frozen.FinalizedDraftRevision = r.ExpectedRevision // Keep the later metadata guard false.
			r.NewCollection = &frozen
		}},
		{"retry cannot start pending", "RetryRound", rl.ImmediateMode, func(r *rl.AdmitRoundRequest) { r.InitialState = contracts.PendingSchedule }},
		{"due cannot start pending", "ExecuteDue", rl.ReservationMode, func(r *rl.AdmitRoundRequest) { r.InitialState = contracts.PendingSchedule }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, entropy, healthy := directStorageAdmitFixture(t, tc.kind, tc.mode)
			bad := directStorageAdmitCopy(t, healthy)
			tc.mutate(&bad)
			directStorageAdmitRejectAndCorrect(t, store, entropy, healthy, bad)
		})
	}
}

func TestStorageAdmitNewFrozenMetadataRejectsEachIndependentMismatchBeforeWrites(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*rl.AdmitRoundRequest)
	}{
		{"frozen collection owner mismatch", func(r *rl.AdmitRoundRequest) { r.NewCollection.ID = "another-collection" }},
		{"empty source draft", func(r *rl.AdmitRoundRequest) { r.NewCollection.SourceDraftID = "" }},
		{"finalized revision mismatch", func(r *rl.AdmitRoundRequest) { r.NewCollection.FinalizedDraftRevision++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, entropy, healthy := directStorageAdmitFixture(t, "CreateCollection", rl.ImmediateMode)
			bad := directStorageAdmitCopy(t, healthy)
			tc.mutate(&bad)
			directStorageAdmitRejectAndCorrect(t, store, entropy, healthy, bad)
		})
	}
}

// Three participants in all paths. Existing product helpers own DB, service and
// close cleanup; this does not introduce a new driver, time source or framework.
func directStorageAdmitFixture(t *testing.T, kind, mode string) (*Store, *productEntropy, rl.AdmitRoundRequest) {
	t.Helper()
	registerCorruptionReadProbe(t)
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	input := productRequest(storageNow, mode)
	if kind == "CreateCollection" {
		frozen := input.Collection
		frozen.ID = "typed-admit-collection"
		initial := contracts.Executing
		if mode == rl.ReservationMode {
			initial = contracts.PendingSchedule
		}
		return store, entropy, rl.AdmitRoundRequest{
			Operation:    rl.OperationIdentity{ID: "typed-admit-operation", Kind: kind, PublicFingerprint: [32]byte{19}},
			CollectionID: frozen.ID, ExpectedRevision: input.Context.Revision,
			NewCollection: &frozen, RoundID: "typed-admit-round", Number: 1, Attempt: 1,
			Input: input.Input, InitialState: initial, AdmittedAt: storageNow,
		}
	}

	if kind == "RetryRound" {
		entropy.fail.Store(true)
	}
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	before, err := service.CreateCollection(context.Background(), input)
	if kind == "RetryRound" {
		requireFault(t, err, contracts.InvalidState)
		if before.State != contracts.Failed || before.Outcome != nil {
			t.Fatal("fixture must own a healthy durable failed round", before, err)
		}
	} else if err != nil {
		t.Fatal("fixture create failed", err)
	}
	healthy := rl.AdmitRoundRequest{
		Operation:    rl.OperationIdentity{ID: "typed-admit-operation", Kind: kind, PublicFingerprint: [32]byte{19}},
		CollectionID: before.CollectionID, ExpectedRevision: before.Revision, ExpectedVersion: before.Version,
		RoundID: before.ID, Number: before.Number, Attempt: before.Attempt,
		Input: before.Input, InitialState: contracts.Executing, AdmittedAt: storageNow,
	}
	switch kind {
	case "Rerun":
		if before.State != contracts.Completed || before.Outcome == nil || len(before.Outcome.Winners) != 1 || before.Outcome.Winners[0].ParticipantID != "p1" {
			t.Fatal("rerun fixture must have one completed consumed participant", before)
		}
		healthy.RoundID = "typed-rerun-round"
		healthy.Number = before.Number + 1
		healthy.Attempt = 1
		healthy.Input.CandidateIDs = []contracts.ParticipantID{"p2", "p3"}
	case "RetryRound":
		healthy.Attempt = before.Attempt + 1
	case "ExecuteDue":
		if before.State != contracts.PendingSchedule || entropy.calls.Load() != 0 {
			t.Fatal("due fixture must be an unexecuted reservation", before)
		}
		quick := uint32(10)
		scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{
			OperationID: "typed-fixture-schedule", Context: productRoundContext(before, "session-one"),
			QuickDelaySeconds: &quick, Timezone: "Asia/Seoul",
		})
		if err != nil || scheduled.State != contracts.Scheduled || scheduled.ScheduledAt == nil {
			t.Fatal("due fixture scheduling failed", scheduled, err)
		}
		healthy.ExpectedRevision = scheduled.Revision
		healthy.ExpectedVersion = scheduled.Version
		healthy.AdmittedAt = *scheduled.ScheduledAt
		healthy.Session = "session-one"
	default:
		t.Fatal("unknown direct admission fixture kind", kind)
	}
	return store, entropy, healthy
}

// JSON creates independent pointer/slice storage. Mutating a bad snapshot must
// never mutate the expected healthy request (the prior M01 oracle bug).
func directStorageAdmitCopy(t *testing.T, request rl.AdmitRoundRequest) rl.AdmitRoundRequest {
	t.Helper()
	admittedAt := request.AdmittedAt
	request.AdmittedAt = storageNow // JSON Time rejects year 10000 before the port can observe it.
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var copied rl.AdmitRoundRequest
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatal(err)
	}
	copied.AdmittedAt = admittedAt
	return copied
}

func directStorageAdmitRejectAndCorrect(t *testing.T, store *Store, entropy *productEntropy, healthy, bad rl.AdmitRoundRequest) {
	t.Helper()
	// Every AdmitRound write path starts with a collection INSERT or revision
	// UPDATE. This observes actual SQL evaluation outside its rolled-back state.
	// It does not claim that validation performed zero read queries or BeginTx.
	execSQL(t, store.db, `CREATE TRIGGER typed_admit_collection_insert BEFORE INSERT ON collections BEGIN SELECT jackpot_corruption_read_probe('admission-write'); END;
CREATE TRIGGER typed_admit_collection_revision BEFORE UPDATE OF revision ON collections BEGIN SELECT jackpot_corruption_read_probe('admission-write'); END`)
	probe := new(corruptionReadProbe)
	t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
	before := corruptionDurableSnapshot(t, store)
	calls := entropy.calls.Load()
	expectedBad := directStorageAdmitCopy(t, bad)
	expectedHealthy := directStorageAdmitCopy(t, healthy)
	for repeat := 0; repeat < 2; repeat++ {
		probe.calls.Store(0)
		activeCorruptionReadProbe.Store(probe)
		got, err := store.AdmitRound(context.Background(), bad)
		activeCorruptionReadProbe.Store(nil)
		requireFault(t, err, contracts.InvalidInput)
		if !reflect.DeepEqual(got, rl.Admission{}) || probe.calls.Load() != 0 {
			t.Fatal("bad typed entry returned admission or evaluated SQL writes", got, probe.calls.Load())
		}
		assertNoHandles(t, store)
		if before != corruptionDurableSnapshot(t, store) {
			t.Fatal("bad typed entry changed durable tables or operation sequence")
		}
		operation, err := store.ReadOperation(context.Background(), healthy.Operation.ID)
		if err != nil || operation.Status != contracts.OperationUnknown {
			t.Fatal("bad entry consumed the reusable operation ID", operation, err)
		}
		if !reflect.DeepEqual(bad, expectedBad) || entropy.calls.Load() != calls {
			t.Fatal("bad entry mutated caller request or reached entropy", err)
		}
		assertNoHandles(t, store)
	}

	admitted, err := store.AdmitRound(context.Background(), healthy)
	if err != nil || admitted.Replay || admitted.Round.ID != healthy.RoundID || admitted.Round.CollectionID != healthy.CollectionID ||
		admitted.Round.Number != healthy.Number || admitted.Round.Attempt != healthy.Attempt || admitted.Round.State != healthy.InitialState ||
		!reflect.DeepEqual(admitted.Round.Input, healthy.Input) || admitted.Operation.Operation != healthy.Operation ||
		admitted.Operation.CollectionID != healthy.CollectionID || admitted.Operation.RoundID != healthy.RoundID {
		t.Fatal("corrected same operation did not admit the exact healthy request", admitted, err)
	}
	expectedRevision := healthy.ExpectedRevision + 1
	expectedVersion := contracts.RoundVersion(1)
	if healthy.NewCollection != nil {
		expectedRevision = 1
	}
	if healthy.Operation.Kind == "RetryRound" || healthy.Operation.Kind == "ExecuteDue" {
		expectedVersion = healthy.ExpectedVersion + 1
	}
	if admitted.Round.Revision != expectedRevision || admitted.Operation.Revision != expectedRevision || admitted.Round.Version != expectedVersion {
		t.Fatal("corrected admission advanced revision/version incorrectly", admitted)
	}
	assertNoHandles(t, store)
	after := corruptionDurableSnapshot(t, store)
	if before == after {
		t.Fatal("successful corrected admission had no durable write")
	}
	replay, err := store.AdmitRound(context.Background(), healthy)
	if err != nil || !replay.Replay || !reflect.DeepEqual(replay.Round, admitted.Round) || !reflect.DeepEqual(replay.Operation, admitted.Operation) ||
		after != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
		t.Fatal("exact replay changed admission, durable state or entropy", replay, err)
	}
	if !reflect.DeepEqual(healthy, expectedHealthy) {
		t.Fatal("admission mutated the healthy caller request", err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
