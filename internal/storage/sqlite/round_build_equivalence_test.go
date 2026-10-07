package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

const equivalenceOutputEnv = "JACKPOT_BUILD_EQUIVALENCE_OUTPUT"

func equivalenceWorkspace(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for depth := 0; depth < 8; depth++ {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	t.Fatal("equivalence workspace go.mod not found")
	return ""
}
func validateEquivalenceOutput(workspace, output string) (string, error) {
	if output == "" {
		return "", nil
	}
	if !filepath.IsAbs(output) || filepath.Clean(output) != output || filepath.Ext(output) != ".json" {
		return "", errors.New("equivalence output must be a new absolute JSON path")
	}
	parent := filepath.Dir(output)
	task := filepath.Join(workspace, ".task")
	if !strings.EqualFold(filepath.Dir(parent), task) || !strings.HasPrefix(filepath.Base(parent), "equivalence-") || len(filepath.Base(parent)) <= len("equivalence-") {
		return "", errors.New("equivalence output escaped direct owned task directory")
	}
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	realTask, err := filepath.EvalSymlinks(task)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(realTask, filepath.Join(realWorkspace, ".task")) {
		return "", errors.New("equivalence task root real path escaped workspace")
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Dir(realParent), realTask) || filepath.Base(realParent) != filepath.Base(parent) {
		return "", errors.New("equivalence output real parent escaped workspace")
	}
	if _, err = os.Lstat(output); err == nil {
		return "", errors.New("equivalence output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return output, nil
}
func TestRoundBuildEquivalenceOutputRejectsRelativeForeignNestedAndExistingTargets(t *testing.T) {
	workspace := equivalenceWorkspace(t)
	if err := os.MkdirAll(filepath.Join(workspace, ".task"), 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(filepath.Join(workspace, ".task"), "equivalence-path-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !strings.EqualFold(filepath.Dir(directory), filepath.Join(workspace, ".task")) || !strings.HasPrefix(filepath.Base(directory), "equivalence-path-") {
			t.Error("equivalence test cleanup escaped workspace")
			return
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	valid := filepath.Join(directory, "trace.json")
	if result, err := validateEquivalenceOutput(workspace, valid); err != nil || result != valid {
		t.Fatal(result, err)
	}
	if result, err := validateEquivalenceOutput(workspace, ""); err != nil || result != "" {
		t.Fatal(result, err)
	}
	for _, invalid := range []string{"relative.json", filepath.Join(workspace, "foreign.json"), filepath.Join(directory, "nested", "trace.json"), filepath.Join(workspace, ".task", "ordinary", "trace.json"), filepath.Join(directory, "trace.txt"), filepath.Join(directory, "..", "escape.json")} {
		if _, err := validateEquivalenceOutput(workspace, invalid); err == nil {
			t.Fatal("unsafe output accepted", invalid)
		}
	}
	if err := os.WriteFile(valid, []byte("existing immutable test marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEquivalenceOutput(workspace, valid); err == nil {
		t.Fatal("existing target accepted")
	}
	actual, err := os.ReadFile(valid)
	if err != nil || string(actual) != "existing immutable test marker" {
		t.Fatal("validation mutated existing target", err)
	}
}

func equivalenceCounts(t *testing.T, store *Store) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for _, table := range []string{"collections", "participants", "operations", "rounds", "round_candidates", "round_prizes", "round_attempts", "results", "winners", "sqlite_sequence"} {
		result[table] = roundTableCount(t, store, table)
	}
	return result
}
func TestRoundBuildEquivalenceActualSQLiteThreeTwoOneTrace(t *testing.T) {
	output, err := validateEquivalenceOutput(equivalenceWorkspace(t), os.Getenv(equivalenceOutputEnv))
	if err != nil {
		t.Fatal(err)
	}
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)

	var notices []contracts.StateNotice
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", func(_ context.Context, notice contracts.StateNotice) error {
		notices = append(notices, notice)
		return nil
	})
	type step struct {
		Round     rl.RoundRecord     `json:"round"`
		Replay    rl.RoundRecord     `json:"replay"`
		Operation rl.OperationRecord `json:"operation"`
		Counts    map[string]int     `json:"counts"`
	}
	trace := struct {
		Fixture             string                  `json:"fixture"`
		Steps               []step                  `json:"steps"`
		FinalRounds         []rl.RoundRecord        `json:"finalRounds"`
		Revision            contracts.Revision      `json:"revision"`
		Notices             []contracts.StateNotice `json:"notices"`
		FinalCounts         map[string]int          `json:"finalCounts"`
		EntropyCalls        int32                   `json:"entropyCalls"`
		RemainingZeroDenied bool                    `json:"remainingZeroDenied"`
		IntegrityOK         bool                    `json:"integrityOK"`
		ForeignKeysOK       bool                    `json:"foreignKeysOK"`
		ConnectionInUse     int                     `json:"connectionInUse"`
		ClosedConnections   int                     `json:"closedConnections"`
	}{Fixture: "real-sqlite-local-fixed-clock-entropy-three-two-one-v2"}
	ctx := context.Background()
	request := productRequest(now, rl.ImmediateMode)
	var previous rl.RoundRecord
	for index := 0; index < 3; index++ {
		var result, replay rl.RoundRecord
		var operationID contracts.OperationID
		if index == 0 {
			operationID = request.OperationID
			result, err = service.CreateCollection(ctx, request)
			if err == nil {
				replay, err = service.CreateCollection(ctx, request)
			}
		} else {
			operationID = contracts.OperationID(fmt.Sprintf("equivalence-rerun-%d", index+1))
			rerun := rl.RerunRequest{OperationID: operationID, Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: previous.CollectionID, Revision: previous.Revision}, Prizes: []rl.Prize{{ID: rl.PrizeID(fmt.Sprintf("gift-%d", index+1)), Name: "상품", Count: 1}}, Message: "축하", Mode: rl.ImmediateMode}
			result, err = service.Rerun(ctx, rerun)
			if err == nil {
				replay, err = service.Rerun(ctx, rerun)
			}
		}
		if err != nil || !reflect.DeepEqual(result, replay) || result.State != contracts.Completed || result.Outcome == nil || len(result.Outcome.Winners) != 1 || result.Outcome.Winners[0].ParticipantID != contracts.ParticipantID(fmt.Sprintf("p%d", index+1)) || len(result.Input.CandidateIDs) != 3-index || result.Number != uint32(index+1) || result.Revision != contracts.Revision(3*(index+1)) || result.Version != 3 || entropy.calls.Load() != int32(index+1) {
			t.Fatal("controlled trace/replay invariant", index, result, err)
		}
		operation, readErr := store.ReadOperation(ctx, operationID)
		if readErr != nil || operation.Status != contracts.OperationSucceeded || operation.RoundID != result.ID || operation.Revision != result.Revision {
			t.Fatal(operation, readErr)
		}
		trace.Steps = append(trace.Steps, step{Round: result, Replay: replay, Operation: operation, Counts: equivalenceCounts(t, store)})
		previous = result
	}
	before := corruptionDurableSnapshot(t, store)
	_, err = service.Rerun(ctx, rl.RerunRequest{OperationID: "equivalence-zero-remaining", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: previous.CollectionID, Revision: previous.Revision}, Prizes: []rl.Prize{{ID: "none", Count: 1}}, Mode: rl.ImmediateMode})
	requireFault(t, err, contracts.InvalidInput)
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
		t.Fatal("remaining zero created operation or executed")
	}
	_, trace.FinalRounds, trace.Revision, err = store.ReadCollectionState(ctx, previous.CollectionID)
	if err != nil || len(trace.FinalRounds) != 3 || trace.Revision != 9 {
		t.Fatal(trace.FinalRounds, trace.Revision, err)
	}
	trace.Notices = notices
	trace.FinalCounts = equivalenceCounts(t, store)
	if trace.FinalCounts["collections"] != 1 || trace.FinalCounts["participants"] != 3 || trace.FinalCounts["operations"] != 3 || trace.FinalCounts["rounds"] != 3 || trace.FinalCounts["round_candidates"] != 6 || trace.FinalCounts["round_prizes"] != 3 || trace.FinalCounts["round_attempts"] != 3 || trace.FinalCounts["results"] != 3 || trace.FinalCounts["winners"] != 3 || len(notices) != 6 {
		t.Fatal("canonical durable counts", trace.FinalCounts)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
	trace.IntegrityOK = true
	trace.ForeignKeysOK = true
	trace.RemainingZeroDenied = true
	trace.ConnectionInUse = store.db.Stats().InUse
	trace.EntropyCalls = entropy.calls.Load()

	closing, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := service.Close(closing); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	trace.ClosedConnections = store.db.Stats().OpenConnections
	if trace.ClosedConnections != 0 {
		t.Fatal("closed fixture leaked connection")
	}
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"credential", "verifier", store.path} {
		if bytes.Contains(bytes.ToLower(raw), bytes.ToLower([]byte(private))) {
			t.Fatal("equivalence public trace contains private field")
		}
	}
	if output != "" {
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
	}
}
