package contracts_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func assertCode(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != code || fault.MessageKey != string(code) {
		t.Fatalf("got %v, want fault %s", err, code)
	}
	if err.Error() != string(code) {
		t.Fatal("unsafe error message")
	}
}

func TestOpaqueIDRejectsEmptyAndAcceptsNonempty(t *testing.T) {
	assertCode(t, contracts.ValidateID(contracts.DraftID("")), contracts.InvalidInput)
	for _, id := range []contracts.DraftID{"a", "draft-한글", "0"} {
		if err := contracts.ValidateID(id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCounterSafeIntegerBoundaries(t *testing.T) {
	for _, value := range []uint64{0, 1, contracts.MaxSafeInteger - 1, contracts.MaxSafeInteger} {
		if err := contracts.ValidateCounter(contracts.Revision(value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []uint64{contracts.MaxSafeInteger + 1, math.MaxUint64} {
		assertCode(t, contracts.ValidateCounter(contracts.Revision(value)), contracts.InvalidInput)
	}
}

func TestIncrementDoesNotOverflowOrAlterFailedInput(t *testing.T) {
	for _, value := range []uint64{0, 1, contracts.MaxSafeInteger - 1} {
		got, err := contracts.Increment(contracts.Revision(value))
		if err != nil || uint64(got) != value+1 {
			t.Fatalf("got %d %v", got, err)
		}
	}
	for _, value := range []uint64{contracts.MaxSafeInteger, contracts.MaxSafeInteger + 1, math.MaxUint64} {
		got, err := contracts.Increment(contracts.Revision(value))
		assertCode(t, err, contracts.InvalidInput)
		if got != contracts.Revision(value) {
			t.Fatal("failed increment changed value")
		}
	}
}

func TestDistinctCounterTypesShareBounds(t *testing.T) {
	if value, err := contracts.Increment(contracts.ArticleGeneration(0)); value != 1 || err != nil {
		t.Fatal(value, err)
	}
	if value, err := contracts.Increment(contracts.RoundVersion(0)); value != 1 || err != nil {
		t.Fatal(value, err)
	}
}

func validContext() contracts.DraftContext {
	return contracts.DraftContext{BackendSessionID: "session", DraftID: "draft", Revision: 4, ArticleGeneration: 2}
}

func TestDraftContextMatchesWithoutChangingInputs(t *testing.T) {
	current, expected := validContext(), validContext()
	for range 2 {
		if err := contracts.CheckDraftContext(current, expected); err != nil {
			t.Fatal(err)
		}
		if current != validContext() || expected != validContext() {
			t.Fatal("context mutated")
		}
	}
}

func TestEachDraftContextDimensionIndependentlyRejectsStaleCommand(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*contracts.DraftContext)
		code   contracts.ErrorCode
	}{
		{"session", func(c *contracts.DraftContext) { c.BackendSessionID = "old" }, contracts.BackendSessionChanged},
		{"draft", func(c *contracts.DraftContext) { c.DraftID = "old" }, contracts.StaleArticleContext},
		{"generation", func(c *contracts.DraftContext) { c.ArticleGeneration = 1 }, contracts.StaleArticleContext},
		{"revision", func(c *contracts.DraftContext) { c.Revision = 3 }, contracts.StaleRevision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, expected := validContext(), validContext()
			tt.mutate(&expected)
			before := expected
			assertCode(t, contracts.CheckDraftContext(current, expected), tt.code)
			if current != validContext() || expected != before {
				t.Fatal("rejection mutated context")
			}
		})
	}
}

func TestInvalidDraftContextIsRejectedBeforeComparison(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*contracts.DraftContext)
	}{
		{"empty session", func(c *contracts.DraftContext) { c.BackendSessionID = "" }},
		{"empty draft", func(c *contracts.DraftContext) { c.DraftID = "" }},
		{"unsafe revision", func(c *contracts.DraftContext) { c.Revision = contracts.Revision(contracts.MaxSafeInteger + 1) }},
		{"unsafe generation", func(c *contracts.DraftContext) {
			c.ArticleGeneration = contracts.ArticleGeneration(contracts.MaxSafeInteger + 1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := validContext()
			tt.mutate(&current)
			assertCode(t, contracts.CheckDraftContext(current, validContext()), contracts.InvalidState)
			expected := validContext()
			tt.mutate(&expected)
			assertCode(t, contracts.CheckDraftContext(validContext(), expected), contracts.InvalidInput)
		})
	}
}

func TestRoundStateValidationAndGateAreTotal(t *testing.T) {
	for _, tt := range []struct {
		state contracts.RoundState
		gate  bool
	}{
		{contracts.PendingSchedule, true}, {contracts.Scheduled, true}, {contracts.Executing, true}, {contracts.Failed, true},
		{contracts.Completed, false}, {contracts.Cancelled, false},
	} {
		t.Run(string(tt.state), func(t *testing.T) {
			if err := tt.state.Validate(); err != nil {
				t.Fatal(err)
			}
			got, err := tt.state.OccupiesGate()
			if err != nil || got != tt.gate {
				t.Fatal(got, err)
			}
		})
	}
	for _, state := range []contracts.RoundState{"", "unknown"} {
		assertCode(t, state.Validate(), contracts.InvalidState)
		got, err := state.OccupiesGate()
		assertCode(t, err, contracts.InvalidState)
		if got {
			t.Fatal("invalid state silently occupied gate")
		}
	}
}

func TestAllRoundStateTransitionPairs(t *testing.T) {
	// Independent specification table, including all invalid source/target combinations.
	allowed := map[[2]contracts.RoundState]bool{
		{contracts.PendingSchedule, contracts.Scheduled}: true,
		{contracts.PendingSchedule, contracts.Cancelled}: true,
		{contracts.Scheduled, contracts.Executing}:       true,
		{contracts.Scheduled, contracts.Cancelled}:       true,
		{contracts.Executing, contracts.Completed}:       true,
		{contracts.Executing, contracts.Failed}:          true,
		{contracts.Failed, contracts.Executing}:          true,
	}
	states := []contracts.RoundState{"", "unknown", contracts.PendingSchedule, contracts.Scheduled, contracts.Executing, contracts.Completed, contracts.Cancelled, contracts.Failed}
	for _, from := range states {
		for _, to := range states {
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				err := contracts.CheckTransition(from, to)
				if allowed[[2]contracts.RoundState{from, to}] {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					assertCode(t, err, contracts.InvalidState)
				}
			})
		}
	}
}

func TestDraftStateRejectsZeroAndUnknown(t *testing.T) {
	for _, state := range []contracts.DraftState{contracts.DraftEmpty, contracts.DraftLoading, contracts.DraftReady, contracts.DraftFinalized} {
		if err := state.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []contracts.DraftState{"", "unknown"} {
		assertCode(t, state.Validate(), contracts.InvalidState)
	}
}

func TestOperationStateRejectsZeroAndUnknownTag(t *testing.T) {
	for _, state := range []contracts.OperationState{contracts.OperationUnknown, contracts.OperationPending, contracts.OperationSucceeded, contracts.OperationFailed} {
		if err := state.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []contracts.OperationState{"", "unrecognized"} {
		assertCode(t, state.Validate(), contracts.InvalidState)
	}
}

func FuzzRoundStateValidationIsTotal(f *testing.F) {
	for _, seed := range []string{"", "unknown", "scheduled", "executing", "completed"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1024 {
			return
		}
		state := contracts.RoundState(raw)
		valid := raw == "pending_schedule" || raw == "scheduled" || raw == "executing" || raw == "completed" || raw == "cancelled" || raw == "failed"
		err := state.Validate()
		if (err == nil) != valid {
			t.Fatal("incorrect state classification")
		}
		if !valid {
			assertCode(t, err, contracts.InvalidState)
		}
	})
}
