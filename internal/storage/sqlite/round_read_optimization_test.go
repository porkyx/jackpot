package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestRoundReadOptimizationPreservesCorruptionAndOperationFailurePriority(t *testing.T) {
	registerCorruptionReadProbe(t)
	cases := []struct {
		name                                              string
		completed, badInput, badOutcome, operationFailure bool
	}{
		{name: "healthy completed operation failure", completed: true, operationFailure: true},
		{name: "invalid completed input precedes operation failure", completed: true, badInput: true, operationFailure: true},
		{name: "invalid input and outcome precede operation failure", completed: true, badInput: true, badOutcome: true, operationFailure: true},
		{name: "valid input invalid outcome retains operation failure priority", completed: true, badOutcome: true, operationFailure: true},
		{name: "invalid outcome remains rejected after valid operation", completed: true, badOutcome: true},
		{name: "pending invalid input precedes operation failure", badInput: true, operationFailure: true},
		{name: "pending valid input retains operation failure", operationFailure: true},
		{name: "healthy completed remains detached and equal", completed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			mode := rl.ReservationMode
			if tc.completed {
				mode = rl.ImmediateMode
			}
			original, err := service.CreateCollection(context.Background(), productRequest(storageNow, mode))
			if err != nil {
				t.Fatal(err)
			}
			corruptionMode(t, store)
			if tc.badInput {
				execSQL(t, store.db, `UPDATE rounds SET input_json=json_set(input_json,'$.Mode','unknown')`)
			}
			if tc.badOutcome {
				execSQL(t, store.db, `UPDATE results SET outcome_json=json_set(outcome_json,'$.AppVersion','')`)
			}
			execSQL(t, store.db, `ALTER TABLE operations RENAME TO optimized_operation_rows;
   CREATE VIEW operations AS SELECT creation_key,jackpot_corruption_read_probe(operation_id) AS operation_id,kind,collection_id,round_id,status,revision,public_fingerprint,failure_code FROM optimized_operation_rows`)
			before := corruptionDurableSnapshot(t, store)
			rng := entropy.calls.Load()
			probe := &corruptionReadProbe{mode: "once"}
			if tc.operationFailure {
				probe.at = 1
			}
			activeCorruptionReadProbe.Store(probe)
			t.Cleanup(func() { activeCorruptionReadProbe.Store(nil) })
			actual, err := store.ReadRound(context.Background(), original.ID)
			activeCorruptionReadProbe.Store(nil)
			var fault contracts.Fault
			switch {
			case tc.badInput:
				requireFault(t, err, contracts.InvalidState)
				if probe.calls.Load() != 0 {
					t.Fatal("invalid input advanced to SQL dependency", probe.calls.Load())
				}
			case tc.operationFailure:
				if err == nil || errors.As(err, &fault) || !strings.Contains(err.Error(), "injected actual SQLite read failure") || probe.calls.Load() == 0 {
					t.Fatal("later outcome hid original dependency failure", err, probe.calls.Load())
				}
			case tc.badOutcome:
				requireFault(t, err, contracts.InvalidState)
				if probe.calls.Load() == 0 {
					t.Fatal("outcome moved ahead of operation validation")
				}
			default:
				if err != nil || !reflect.DeepEqual(actual, original) {
					t.Fatal("healthy committed output changed", actual, original, err)
				}
				actual.Input.CandidateIDs[0] = "caller-mutated"
				again, readErr := store.ReadRound(context.Background(), original.ID)
				if readErr != nil || !reflect.DeepEqual(again, original) {
					t.Fatal("read exposed borrowed persisted candidate storage", again, readErr)
				}
			}
			if err != nil && !reflect.DeepEqual(actual, rl.RoundRecord{}) {
				t.Fatal("failed read published partial data", actual)
			}
			if before != corruptionDurableSnapshot(t, store) || rng != entropy.calls.Load() {
				t.Fatal("read optimization mutated persistence or used entropy")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestStoredReaderExactSizeLimitAndStrictTrailingWhitespace(t *testing.T) {
	// Syntactically valid JSON at the exact persisted bound must remain accepted;
	// one extra byte is refused before decoding. Whitespace does not hide a token.
	raw := "{}" + strings.Repeat(" ", (100<<20)-2)
	var value rl.RoundInput
	if len(raw) != 100<<20 || decodeStored(raw, &value) != nil {
		t.Fatal("exact stored size bound changed")
	}
	if decodeStored(raw+" ", &value) == nil {
		t.Fatal("stored size limit plus one accepted")
	}
	for _, suffix := range []string{"{}", "null", "0", "bad"} {
		if decodeStored("{} \r\n\t"+suffix, &value) == nil {
			t.Fatal("trailing stored token accepted", suffix)
		}
	}
	if raw[0:2] != "{}" || len(raw) != 100<<20 {
		t.Fatal("decode changed input bytes")
	}
}
