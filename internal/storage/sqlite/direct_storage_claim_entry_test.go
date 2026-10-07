package sqlite

// Direct claim boundary regressions distinguish no-op fences from invalid claimants.
import (
	"context"
	"reflect"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestStorageClaimAttemptEachCounterMismatchIsNoOpAndDoesNotConsumeEntropy(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*rl.ClaimRequest)
	}{
		{name: "zero attempt", mutate: func(r *rl.ClaimRequest) { r.Attempt = 0 }},
		{name: "maximum attempt", mutate: func(r *rl.ClaimRequest) { r.Attempt = ^uint32(0) }},
		{name: "zero version", mutate: func(r *rl.ClaimRequest) { r.ExpectedVersion = 0 }},
		{name: "maximum safe version", mutate: func(r *rl.ClaimRequest) { r.ExpectedVersion = contracts.RoundVersion(contracts.MaxSafeInteger) }},
		{name: "zero revision", mutate: func(r *rl.ClaimRequest) { r.ExpectedRevision = 0 }},
		{name: "maximum safe revision", mutate: func(r *rl.ClaimRequest) { r.ExpectedRevision = contracts.Revision(contracts.MaxSafeInteger) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, round, _, entropy := coreConditionAdmittedRound(t, false)
			operation, err := store.ReadActiveOperation(context.Background(), round.ID)
			if err != nil || operation.Status != contracts.OperationPending {
				t.Fatal("fixture must expose its pending owner", operation, err)
			}
			if round.Attempt != 1 || round.Version != 1 || round.Revision != 1 || round.State != contracts.Executing || round.Claim != nil || entropy.calls.Load() != 0 {
				t.Fatal("claim fixture must isolate mismatch from the positive minimum", round)
			}
			healthy := rl.ClaimRequest{
				CollectionID: round.CollectionID, RoundID: round.ID, OperationID: operation.Operation.ID,
				ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Attempt: round.Attempt, Session: "session-one",
			}
			bad := healthy
			tc.mutate(&bad)
			before := corruptionDurableSnapshot(t, store)
			for repeat := 0; repeat < 2; repeat++ {
				claim, err := store.ClaimAttempt(context.Background(), bad)
				if err != nil || claim != nil {
					t.Fatal("counter mismatch must be a nil-claim no-op", claim, err)
				}
				assertNoHandles(t, store) // Observe a leaked transaction before a snapshot can block.
				if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 || store.db.Stats().InUse != 0 {
					t.Fatal("no-op changed durable state, consumed entropy, or retained a connection")
				}
			}
			claim, err := store.ClaimAttempt(context.Background(), healthy)
			if err != nil || claim == nil || claim.CollectionID != round.CollectionID || claim.RoundID != round.ID ||
				claim.OperationID != operation.Operation.ID || claim.Session != "session-one" || claim.Attempt != round.Attempt || claim.Version != round.Version+1 {
				t.Fatal("corrected request must claim its original owner once", claim, err)
			}
			current, err := store.ReadRound(context.Background(), round.ID)
			if err != nil || current.State != contracts.Executing || current.Claim == nil || *current.Claim != *claim ||
				current.Revision != round.Revision+1 || current.Version != round.Version+1 || current.Outcome != nil {
				t.Fatal("claim must agree with the committed executing round", current, err)
			}
			assertNoHandles(t, store)
			claimedState := corruptionDurableSnapshot(t, store)
			duplicate := healthy
			duplicate.ExpectedRevision = current.Revision
			duplicate.ExpectedVersion = current.Version
			again, err := store.ClaimAttempt(context.Background(), duplicate)
			assertNoHandles(t, store)
			if err != nil || again != nil || claimedState != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("already claimed owner must not admit a duplicate", again, err)
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("claim or duplicate retained a DB connection")
			}
			assertRoundSQLIntegrity(t, store)
		})
	}
}

func TestStorageClaimAttemptInvalidSessionAndUnknownOperationPreserveThePendingOwner(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*rl.ClaimRequest)
	}{
		{name: "empty session", mutate: func(r *rl.ClaimRequest) { r.Session = "" }},
		{name: "empty operation", mutate: func(r *rl.ClaimRequest) { r.OperationID = "" }},
		{name: "unknown operation", mutate: func(r *rl.ClaimRequest) { r.OperationID = "missing-claim-operation" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, round, _, entropy := coreConditionAdmittedRound(t, false)
			operation, err := store.ReadActiveOperation(context.Background(), round.ID)
			if err != nil || operation.Status != contracts.OperationPending {
				t.Fatal("fixture must expose its pending owner", operation, err)
			}
			if round.Attempt != 1 || round.Version != 1 || round.Revision != 1 || round.State != contracts.Executing || round.Claim != nil || entropy.calls.Load() != 0 {
				t.Fatal("claim fixture must isolate mismatch from the positive minimum", round)
			}
			healthy := rl.ClaimRequest{
				CollectionID: round.CollectionID, RoundID: round.ID, OperationID: operation.Operation.ID,
				ExpectedRevision: round.Revision, ExpectedVersion: round.Version, Attempt: round.Attempt, Session: "session-one",
			}
			bad := healthy
			tc.mutate(&bad)
			before := corruptionDurableSnapshot(t, store)
			for repeat := 0; repeat < 2; repeat++ {
				claim, err := store.ClaimAttempt(context.Background(), bad)
				requireFault(t, err, contracts.InvalidState)
				assertNoHandles(t, store)
				if claim != nil || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 || store.db.Stats().InUse != 0 {
					t.Fatal("invalid claimant changed durable state, consumed entropy or retained a connection", claim)
				}
				observed, err := store.ReadActiveOperation(context.Background(), round.ID)
				if err != nil || !reflect.DeepEqual(observed, operation) {
					t.Fatal("invalid claimant changed its actual pending owner", observed, err)
				}
			}
			claim, err := store.ClaimAttempt(context.Background(), healthy)
			if err != nil || claim == nil || claim.OperationID != operation.Operation.ID || claim.Session != "session-one" || entropy.calls.Load() != 0 {
				t.Fatal("corrected request must claim without executing the draw", claim, err)
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("corrected claim retained a DB connection")
			}
			assertRoundSQLIntegrity(t, store)
		})
	}
}
