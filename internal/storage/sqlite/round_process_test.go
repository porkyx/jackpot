package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

const processBoundaryPrefix = "JACKPOT_ROUND_BOUNDARY "

func processBoundary(phase string) {
	fmt.Println(processBoundaryPrefix + phase)
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type processBoundaryStorage struct {
	*Store
	phase string
}

func (storage processBoundaryStorage) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {
	if storage.phase == "before-admission" {
		processBoundary(storage.phase)
	}
	admission, err := storage.Store.AdmitRound(ctx, request)
	if err == nil && storage.phase == "after-admission" {
		processBoundary(storage.phase)
	}
	return admission, err
}
func (storage processBoundaryStorage) ClaimAttempt(ctx context.Context, request rl.ClaimRequest) (*rl.ClaimToken, error) {
	if storage.phase == "before-claim" {
		processBoundary(storage.phase)
	}
	claim, err := storage.Store.ClaimAttempt(ctx, request)
	if err == nil && claim != nil && storage.phase == "after-claim" {
		processBoundary(storage.phase)
	}
	return claim, err
}
func (storage processBoundaryStorage) CommitOutcome(ctx context.Context, request rl.CommitOutcomeRequest) (rl.RoundRecord, error) {
	if storage.phase == "before-commit" {
		processBoundary(storage.phase)
	}
	result, err := storage.Store.CommitOutcome(ctx, request)
	if err == nil && storage.phase == "after-commit" {
		processBoundary(storage.phase)
	}
	return result, err
}
func TestRoundProcessHelper(t *testing.T) {
	phase := os.Getenv("JACKPOT_ROUND_PROCESS_PHASE")
	if phase == "" {
		return
	}
	path := os.Getenv("JACKPOT_ROUND_PROCESS_PATH")
	if !filepath.IsAbs(path) {
		t.Fatal("absolute helper db required")
	}
	if phase == "during-transaction" {
		if err := driverSQLite.RegisterScalarFunction("jackpot_process_boundary", 0, func(_ *driverSQLite.FunctionContext, _ []driver.Value) (driver.Value, error) {
			processBoundary(phase)
			return int64(1), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if phase == "during-transaction" {
		if _, err = store.db.Exec("CREATE TRIGGER test_round_process_barrier AFTER INSERT ON winners BEGIN SELECT jackpot_process_boundary(); END"); err != nil {
			t.Fatal(err)
		}
	}
	var ids atomic.Uint32
	service, err := rl.NewService(rl.ServiceOptions{Storage: processBoundaryStorage{Store: store, phase: phase}, Session: "session-one", Entropy: new(productEntropy), Clock: func() time.Time { return storageNow }, NewID: func() string { return fmt.Sprintf("process-id-%d", ids.Add(1)) }, AppVersion: "process-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	if _, err = service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode)); err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper crossed the requested kill boundary")
}
func killAtRoundBoundary(t *testing.T, path, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRoundProcessHelper$", "-test.timeout=60s")
	command.Env = append(os.Environ(), "JACKPOT_ROUND_PROCESS_PHASE="+phase, "JACKPOT_ROUND_PROCESS_PATH="+path)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != processBoundaryPrefix+phase {
		t.Fatalf("boundary=%q err=%v context=%v stderr=%s", line, err, ctx.Err(), stderr.String())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Success() {
		t.Fatalf("process kill did not produce failed exit: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("helper reached deadline instead of deterministic boundary", ctx.Err())
	}
}
func assertRoundSQLIntegrity(t *testing.T, store *Store) {
	t.Helper()
	var integrity string
	if err := store.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := store.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign key damage")
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
}
func roundTableCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestRoundProcessKillAtAdmissionClaimTransactionAndCommitBoundaries(t *testing.T) {
	// This harness tests forced process termination on a real WAL database.
	// It does not model power failure or an OS/storage device losing flushed data.
	for _, phase := range []string{"before-admission", "after-admission", "before-claim", "after-claim", "before-commit", "during-transaction", "after-commit"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "process-round.sqlite3")
			killAtRoundBoundary(t, path, phase)
			store, err := Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			assertRoundSQLIntegrity(t, store)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
			if phase == "before-admission" {
				for _, table := range []string{"collections", "participants", "rounds", "operations", "results", "winners"} {
					if roundTableCount(t, store, table) != 0 {
						t.Fatal("pre-admission left durable rows", table)
					}
				}
				report, err := service.Recover(context.Background(), rl.RecoverRequest{})
				if err != nil || len(report.Failed) != 0 || len(report.Completed) != 0 || entropy.calls.Load() != 0 {
					t.Fatal(report, err)
				}
				request := productRequest(storageNow, rl.ImmediateMode)
				request.Context.BackendSessionID = "session-two"
				result, err := service.CreateCollection(context.Background(), request)
				if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
					t.Fatal(result, err)
				}
				assertRoundSQLIntegrity(t, store)
				assertNoHandles(t, store)
				return
			}
			before, err := store.ReadRound(context.Background(), "process-id-2")
			if err != nil {
				t.Fatal(err)
			}
			completed := phase == "after-commit"
			if completed {
				if before.State != contracts.Completed || before.Outcome == nil || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
					t.Fatal(before)
				}
			} else {
				if before.State != contracts.Executing || before.Outcome != nil || roundTableCount(t, store, "results") != 0 || roundTableCount(t, store, "winners") != 0 {
					t.Fatal("partial result after kill", before)
				}
				claimExpected := phase != "after-admission" && phase != "before-claim"
				if (before.Claim != nil) != claimExpected {
					t.Fatal("wrong durable claim boundary", before)
				}
			}
			report, err := service.Recover(context.Background(), rl.RecoverRequest{})
			if err != nil || entropy.calls.Load() != 0 {
				t.Fatal("recovery sampled entropy", report, err)
			}
			after, err := store.ReadRound(context.Background(), before.ID)
			if err != nil {
				t.Fatal(err)
			}
			request := productRequest(storageNow, rl.ImmediateMode)
			request.Context.BackendSessionID = "session-two"
			replay, replayErr := service.CreateCollection(context.Background(), request)
			if entropy.calls.Load() != 0 {
				t.Fatal("replay sampled or granted management authority")
			}
			if completed {
				if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(before, replay) || replayErr != nil || len(report.Failed) != 0 {
					t.Fatal("completed result changed after restart", report, replayErr)
				}
			} else {
				if len(report.Failed) != 1 || report.Failed[0] != before.ID || after.State != contracts.Failed || after.Outcome != nil || replay.ID != before.ID {
					t.Fatal(after, report, replay, replayErr)
				}
				requireFault(t, replayErr, contracts.InvalidState)
				op, err := store.ReadOperation(context.Background(), "create-op")
				if err != nil || op.Status != contracts.OperationFailed {
					t.Fatal(op, err)
				}
				if phase == "during-transaction" {
					if _, err = store.db.Exec("DROP TRIGGER test_round_process_barrier"); err != nil {
						t.Fatal(err)
					}
				}

				retry, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "restart-retry", Context: productRoundContext(after, "session-two")})
				if err != nil || retry.ID != before.ID || retry.Attempt != 2 || retry.State != contracts.Completed || !reflect.DeepEqual(retry.Input, before.Input) || entropy.calls.Load() != 1 {
					t.Fatal(retry, err)
				}
			}
			if roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
				t.Fatal("duplicate or absent final result")
			}
			second, err := service.Recover(context.Background(), rl.RecoverRequest{})
			if err != nil || len(second.Failed) != 0 || len(second.Due) != 0 {
				t.Fatal("repeated recovery changed final state", second, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
