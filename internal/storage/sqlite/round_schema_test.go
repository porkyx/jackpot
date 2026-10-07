package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"

	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

var storageNow = time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)

func jsonValue(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func execSQL(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func seedRound(t *testing.T, store *Store, c, r, op, state string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	frozen := rl.FrozenCollection{ID: contracts.CollectionID(c), SourceDraftID: contracts.DraftID("draft-" + c), FinalizedDraftRevision: 3, ArticleGeneration: 2, Snapshot: contracts.SnapshotSummary{SnapshotID: "snapshot", Complete: true, CollectedAt: storageNow, Pages: 1, AcceptedComments: 2}, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "제목"}, Filters: rl.FilterSnapshot{RulesVersion: "1"}}

	execSQL(t, tx, "INSERT INTO collections(id,source_draft_id,revision,frozen_json,created_at) VALUES (?,?,1,?,?)", c, "draft-"+c, jsonValue(t, frozen), storageNow.Format(time.RFC3339Nano))
	for i := 1; i <= 3; i++ {
		p := rl.ParticipantSnapshot{ID: contracts.ParticipantID(fmt.Sprintf("p%d", i)), Nickname: fmt.Sprintf("참가자%d", i), Included: i < 3, Comments: []rl.CommentSnapshot{{ID: fmt.Sprintf("comment-%d", i), Kind: "text", Text: "댓글"}}}
		execSQL(t, tx, "INSERT INTO participants(collection_id,id,position,included,body_json) VALUES (?,?,?,?,?)", c, p.ID, i-1, p.Included, jsonValue(t, p))
	}
	input := rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p1", "p2"}, Prizes: []rl.Prize{{ID: "gift", Name: "상품", Count: 1}}, Message: "메시지", Mode: "immediate"}
	execSQL(t, tx, "INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES (?,?,1,?,1,1,?,?)", c, r, state, jsonValue(t, input), op)
	execSQL(t, tx, "INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES (?,'CreateCollection',?,?,'pending',1,zeroblob(32))", op, c, r)
	for i, id := range input.CandidateIDs {
		execSQL(t, tx, "INSERT INTO round_candidates VALUES (?,?,?,?)", c, r, id, i)
	}
	execSQL(t, tx, "INSERT INTO round_prizes VALUES (?,?,'gift',0,1)", c, r)
	execSQL(t, tx, "INSERT INTO round_attempts(collection_id,round_id,attempt) VALUES (?,?,1)", c, r)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func outcomeValue() rl.Outcome {
	return rl.Outcome{Winners: []rl.Winner{{ParticipantID: "p1", PrizeID: "gift", Slot: 1}}, ExecutedAt: storageNow, AlgorithmVersion: "1", AppVersion: "test"}
}
func finishRound(t *testing.T, store *Store, c, r, op string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execSQL(t, tx, "INSERT INTO results VALUES (?,?,?)", c, r, jsonValue(t, outcomeValue()))
	execSQL(t, tx, "INSERT INTO winners VALUES (?,?,'p1','gift',1)", c, r)
	execSQL(t, tx, "UPDATE collections SET revision=revision+1 WHERE id=?", c)
	execSQL(t, tx, "UPDATE rounds SET state='completed',version=version+1 WHERE id=?", r)
	execSQL(t, tx, "UPDATE operations SET status='succeeded',revision=2 WHERE operation_id=?", op)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func assertNoHandles(t *testing.T, store *Store) {
	t.Helper()
	if store.db.Stats().InUse != 0 {
		t.Fatal("connection/transaction leaked")
	}
}
func rejectedSQL(t *testing.T, store *Store, query string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(query)
	if err == nil {
		err = tx.Commit()
	} else {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}
	if err == nil {
		t.Fatalf("constraint accepted %s", query)
	}
	_ = tx.Rollback()
	assertNoHandles(t, store)
}

func TestDurableSchemaRejectsIdentityBoundsAndCompositeKeyViolations(t *testing.T) {
	cases := map[string]string{
		"same source draft":                     `INSERT INTO collections VALUES ('new','draft-c1',1,'{}','now')`,
		"empty collection":                      `INSERT INTO collections VALUES ('','other',1,'{}','now')`,
		"null collection":                       `INSERT INTO collections VALUES (NULL,'other',1,'{}','now')`,
		"zero collection revision":              `UPDATE collections SET revision=0 WHERE id='c1'`,
		"unsafe collection revision":            `UPDATE collections SET revision=9007199254740992 WHERE id='c1'`,
		"negative candidate order":              `INSERT INTO round_candidates VALUES ('c1','r1','p3',-1)`,
		"excluded candidate":                    `INSERT INTO round_candidates VALUES ('c1','r1','p3',2)`,
		"missing candidate":                     `INSERT INTO round_candidates VALUES ('c1','r1','missing',2)`,
		"candidate in another collection":       `INSERT INTO round_candidates VALUES ('c1','r1','foreign',2)`,
		"duplicate candidate":                   `INSERT INTO round_candidates VALUES ('c1','r1','p1',2)`,
		"duplicate candidate position":          `INSERT INTO round_candidates VALUES ('c1','r1','p2',0)`,
		"candidate wrong round owner":           `INSERT INTO round_candidates VALUES ('c1','r2','p1',2)`,
		"participant empty ID":                  `INSERT INTO participants VALUES ('c1','',3,1,'{}')`,
		"participant missing owner":             `INSERT INTO participants VALUES ('missing','new',3,1,'{}')`,
		"participant bad included":              `INSERT INTO participants VALUES ('c1','new',3,2,'{}')`,
		"participant bad JSON":                  `INSERT INTO participants VALUES ('c1','new',3,1,'bad')`,
		"participant duplicate position":        `INSERT INTO participants VALUES ('c1','new',0,1,'{}')`,
		"prize zero count":                      `INSERT INTO round_prizes VALUES ('c1','r1','new',1,0)`,
		"prize count eleven":                    `INSERT INTO round_prizes VALUES ('c1','r1','new',1,11)`,
		"prize negative position":               `INSERT INTO round_prizes VALUES ('c1','r1','new',-1,1)`,
		"prize duplicate order":                 `INSERT INTO round_prizes VALUES ('c1','r1','new',0,1)`,
		"prize empty ID":                        `INSERT INTO round_prizes VALUES ('c1','r1','',1,1)`,
		"prize wrong round owner":               `INSERT INTO round_prizes VALUES ('c1','r2','new',1,1)`,
		"round zero version":                    `UPDATE rounds SET version=0 WHERE id='r1'`,
		"round unsafe version":                  `UPDATE rounds SET version=9007199254740992 WHERE id='r1'`,
		"round zero attempt":                    `UPDATE rounds SET attempt=0 WHERE id='r1'`,
		"round attempt overflow":                `UPDATE rounds SET attempt=4294967296 WHERE id='r1'`,
		"round unknown state":                   `UPDATE rounds SET state='unknown' WHERE id='r1'`,
		"round invalid claim JSON":              `UPDATE rounds SET claim_json='bad' WHERE id='r1'`,
		"round operation in another collection": `UPDATE rounds SET active_operation_id='op2' WHERE id='r1'`,
		"round absent operation":                `UPDATE rounds SET active_operation_id='missing' WHERE id='r1'`,
		"operation empty ID":                    `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('','Rerun','c1','r1','pending',1,zeroblob(32))`,
		"operation unknown kind":                `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('new','unknown','c1','r1','pending',1,zeroblob(32))`,
		"operation unknown status":              `UPDATE operations SET status='unknown' WHERE operation_id='op1'`,
		"operation negative revision":           `UPDATE operations SET revision=-1 WHERE operation_id='op1'`,
		"operation unsafe revision":             `UPDATE operations SET revision=9007199254740992 WHERE operation_id='op1'`,
		"operation wrong fingerprint length":    `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('new','Rerun','c1','r1','pending',1,zeroblob(31))`,
		"operation wrong round owner":           `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('new','Rerun','c1','r2','pending',1,zeroblob(32))`,
		"operation missing round":               `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('new','Rerun','c1','missing','pending',1,zeroblob(32))`,
		"attempt zero":                          `INSERT INTO round_attempts(collection_id,round_id,attempt) VALUES ('c1','r1',0)`,
		"attempt overflow":                      `INSERT INTO round_attempts(collection_id,round_id,attempt) VALUES ('c1','r1',4294967296)`,
		"attempt wrong owner":                   `INSERT INTO round_attempts(collection_id,round_id,attempt) VALUES ('c1','r2',2)`,
		"attempt bad claim JSON":                `UPDATE round_attempts SET claim_json='bad' WHERE collection_id='c1' AND round_id='r1'`,
		"result wrong round owner":              `INSERT INTO results VALUES ('c1','r2','{}')`,
		"result invalid JSON":                   `INSERT INTO results VALUES ('c1','r1','bad')`,
		"winner missing result":                 `INSERT INTO winners VALUES ('c1','r1','p1','gift',1)`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			seedRound(t, store, "c2", "r2", "op2", "executing")
			execSQL(t, store.db, `INSERT INTO participants VALUES ('c2','foreign',3,1,'{}')`)
			rejectedSQL(t, store, query)
			record, err := store.ReadOperation(context.Background(), "op1")
			if err != nil || record.Status != contracts.OperationPending || record.Revision != 1 {
				t.Fatalf("original changed: %+v/%v", record, err)
			}
		})
	}
}

func TestDurableGateAndEveryStateTransition(t *testing.T) {
	states := []string{"pending_schedule", "scheduled", "executing", "completed", "cancelled", "failed"}
	for _, from := range states {
		for _, to := range states {
			t.Run(from+" to "+to, func(t *testing.T) {
				store := migratedTestStore(t)
				seedRound(t, store, "c1", "r1", "op1", from)
				allowed := from == to && from != "completed" && from != "cancelled" || from == "pending_schedule" && (to == "scheduled" || to == "cancelled") || from == "scheduled" && (to == "executing" || to == "cancelled") || from == "executing" && (to == "completed" || to == "failed") || from == "failed" && to == "executing"
				_, err := store.db.Exec("UPDATE rounds SET state=? WHERE id='r1'", to)
				if (err == nil) != allowed {
					t.Fatalf("transition %s -> %s: %v", from, to, err)
				}
				var state string
				if err := store.db.QueryRow("SELECT state FROM rounds WHERE id='r1'").Scan(&state); err != nil {
					t.Fatal(err)
				}
				want := from
				if allowed {
					want = to
				}
				if state != want {
					t.Fatal("invalid transition changed state")
				}
				assertNoHandles(t, store)
			})
		}
	}
	for _, state := range states {
		t.Run("gate "+state, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", state)
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.Exec(`INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES ('c1','new',2,'executing',1,1,'{}','new-op')`)
			occupies := state != "completed" && state != "cancelled"
			if (err != nil) != occupies {
				t.Fatalf("gate %s: %v", state, err)
			}
			if err == nil {
				execSQL(t, tx, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('new-op','Rerun','c1','new','pending',1,zeroblob(32))`)
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			assertNoHandles(t, store)
		})
	}
}

func TestDurableSnapshotsResultsAndTerminalOperationsCannotBeChangedOrDeleted(t *testing.T) {
	cases := map[string]string{
		"collection snapshot":           `UPDATE collections SET frozen_json='{}' WHERE id='c1'`,
		"collection source":             `UPDATE collections SET source_draft_id='other' WHERE id='c1'`,
		"collection deletion":           `DELETE FROM collections WHERE id='c1'`,
		"participant update":            `UPDATE participants SET included=0 WHERE collection_id='c1'`,
		"participant deletion":          `DELETE FROM participants WHERE collection_id='c1'`,
		"round input":                   `UPDATE rounds SET input_json='{}' WHERE id='r1'`,
		"round ID":                      `UPDATE rounds SET id='other' WHERE id='r1'`,
		"round number":                  `UPDATE rounds SET number=2 WHERE id='r1'`,
		"round deletion":                `DELETE FROM rounds WHERE id='r1'`,
		"terminal round version":        `UPDATE rounds SET version=version+1 WHERE id='r1'`,
		"candidate update":              `UPDATE round_candidates SET position=9 WHERE collection_id='c1'`,
		"candidate deletion":            `DELETE FROM round_candidates WHERE collection_id='c1'`,
		"prize update":                  `UPDATE round_prizes SET requested_count=2 WHERE collection_id='c1'`,
		"prize deletion":                `DELETE FROM round_prizes WHERE collection_id='c1'`,
		"operation identity":            `UPDATE operations SET public_fingerprint=zeroblob(32) WHERE operation_id='op1'`,
		"operation generation key":      `UPDATE operations SET creation_key=100 WHERE operation_id='op1'`,
		"operation deletion":            `DELETE FROM operations WHERE operation_id='op1'`,
		"terminal operation revision":   `UPDATE operations SET revision=3 WHERE operation_id='op1'`,
		"result update":                 `UPDATE results SET outcome_json='{}' WHERE round_id='r1'`,
		"result deletion":               `DELETE FROM results WHERE round_id='r1'`,
		"result duplicate":              `INSERT INTO results VALUES ('c1','r1','{}')`,
		"winner update":                 `UPDATE winners SET participant_id='p2' WHERE collection_id='c1'`,
		"winner deletion":               `DELETE FROM winners WHERE collection_id='c1'`,
		"winner duplicated participant": `INSERT INTO winners VALUES ('c1','r1','p1','gift',1)`,
		"winner duplicated slot":        `INSERT INTO winners VALUES ('c1','r1','p2','gift',1)`,
		"winner above requested slot":   `INSERT INTO winners VALUES ('c1','r1','p2','gift',2)`,
		"winner zero slot":              `INSERT INTO winners VALUES ('c1','r1','p2','gift',0)`,
		"winner unknown prize":          `INSERT INTO winners VALUES ('c1','r1','p2','missing',1)`,
		"winner not a candidate":        `INSERT INTO winners VALUES ('c1','r1','p3','gift',1)`,
		"attempt deletion":              `DELETE FROM round_attempts WHERE collection_id='c1'`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			finishRound(t, store, "c1", "r1", "op1")
			rejectedSQL(t, store, query)
			record, err := store.ReadRound(context.Background(), "r1")
			if err != nil || record.State != contracts.Completed || record.Outcome == nil || len(record.Outcome.Winners) != 1 {
				t.Fatalf("original outcome changed: %+v/%v", record, err)
			}
		})
	}
}

func TestConditionalExecutionClaimHasExactlyOneOwnerWithoutSleep(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	start := make(chan struct{})
	var owners atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			token := rl.ClaimToken{CollectionID: "c1", RoundID: "r1", OperationID: "op1", Session: contracts.BackendSessionID(fmt.Sprintf("owner-%d", index)), Attempt: 1, Version: 2}
			raw, err := json.Marshal(token)
			if err != nil {
				failures <- err
				return
			}
			result, err := store.db.Exec(`UPDATE rounds SET claim_json=?,version=version+1 WHERE id='r1' AND state='executing' AND version=1 AND attempt=1 AND active_operation_id='op1' AND claim_json IS NULL`, string(raw))
			if err != nil {
				failures <- err
				return
			}
			rows, err := result.RowsAffected()
			if err != nil {
				failures <- err
				return
			}
			if rows > 1 {
				failures <- fmt.Errorf("claim affected %d rows", rows)
			}
			owners.Add(int32(rows))
		}(i)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if owners.Load() != 1 {
		t.Fatalf("duplicate or missing claim: %d", owners.Load())
	}
	record, err := store.ReadRound(context.Background(), "r1")
	if err != nil || record.Claim == nil || record.Version != 2 {
		t.Fatalf("claim lost: %+v/%v", record, err)
	}
	assertNoHandles(t, store)
}

func TestFinalTransactionFirstNthContinuousAndDeferredCommitFailuresPreserveNoOutcome(t *testing.T) {
	steps := []string{
		`INSERT INTO results VALUES ('c1','r1','` + jsonValue(t, outcomeValue()) + `')`,
		`INSERT INTO winners VALUES ('c1','r1','p1','gift',1)`,
		`UPDATE collections SET revision=2 WHERE id='c1'`,
		`UPDATE rounds SET state='completed',version=2 WHERE id='r1'`,
		`UPDATE operations SET status='succeeded',revision=2 WHERE operation_id='op1'`,
	}
	for failureAt := 0; failureAt <= len(steps); failureAt++ {
		t.Run(fmt.Sprintf("failure-%d", failureAt+1), func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			for attempt := 0; attempt < 3; attempt++ {
				tx, err := store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				var failure error
				for index, step := range steps {
					if index == failureAt {
						step = "INVALID FINAL COMMIT SQL"
					}
					_, failure = tx.Exec(step)
					if failure != nil {
						break
					}
				}
				if failureAt == len(steps) {
					// Real deferred FK failure occurs at Commit, after every result write.
					execSQL(t, tx, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('orphan','Rerun','c1','missing','pending',2,zeroblob(32))`)
					failure = tx.Commit()
				} else {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				if failure == nil {
					t.Fatal("injected failure was hidden")
				}
				_ = tx.Rollback()
				round, err := store.ReadRound(context.Background(), "r1")
				if err != nil || round.State != contracts.Executing || round.Outcome != nil || round.Revision != 1 || round.Version != 1 {
					t.Fatalf("partial outcome: %+v/%v", round, err)
				}
				operation, err := store.ReadOperation(context.Background(), "op1")
				if err != nil || operation.Status != contracts.OperationPending || operation.Revision != 1 {
					t.Fatalf("partial operation: %+v/%v", operation, err)
				}
				var count int
				for _, table := range []string{"results", "winners"} {
					if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("partial %s: %d/%v", table, count, err)
					}
				}
				assertNoHandles(t, store)
			}
			finishRound(t, store, "c1", "r1", "op1")
			reopen, err := Open(context.Background(), store.path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopen.Close()
			round, err := reopen.ReadRound(context.Background(), "r1")
			if err != nil || round.State != contracts.Completed || round.Outcome == nil || round.Revision != 2 {
				t.Fatalf("safe retry not durable: %+v/%v", round, err)
			}
		})
	}
}

func TestTypedReadDuringUncommittedFinalTransactionReturnsCommittedVersion(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	writer, err := Open(context.Background(), store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execSQL(t, tx, "INSERT INTO results VALUES ('c1','r1',?)", jsonValue(t, outcomeValue()))
	execSQL(t, tx, `INSERT INTO winners VALUES ('c1','r1','p1','gift',1)`)
	execSQL(t, tx, `UPDATE collections SET revision=2 WHERE id='c1'`)
	execSQL(t, tx, `UPDATE rounds SET state='completed',version=2 WHERE id='r1'`)
	execSQL(t, tx, `UPDATE operations SET status='succeeded',revision=2 WHERE operation_id='op1'`)
	pending, err := store.ReadRound(context.Background(), "r1")
	if err != nil || pending.State != contracts.Executing || pending.Outcome != nil || pending.Revision != 1 {
		t.Fatalf("uncommitted outcome leaked: %+v/%v", pending, err)
	}
	collection, err := store.ReadCollection(context.Background(), "c1")
	if err != nil || len(collection.Participants) != 3 {
		t.Fatalf("read snapshot blocked by uncommitted writer: %+v/%v", collection, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	done, err := store.ReadRound(context.Background(), "r1")
	if err != nil || done.State != contracts.Completed || done.Outcome == nil || done.Revision != 2 || done.Version != 2 {
		t.Fatalf("mixed committed version: %+v/%v", done, err)
	}
	// A lost response does not undo a completed commit or permit a new outcome.
	replay, err := store.ReadRound(context.Background(), "r1")
	if err != nil || jsonValue(t, replay) != jsonValue(t, done) {
		t.Fatal("committed query replay changed outcome")
	}
	assertNoHandles(t, store)
	assertNoHandles(t, writer)
}

func TestConsumedParticipantAndClosedAttemptCannotBeReused(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	finishRound(t, store, "c1", "r1", "op1")
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execSQL(t, tx, `INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES ('c1','r-new',2,'executing',1,1,'{}','op-new')`)
	execSQL(t, tx, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('op-new','Rerun','c1','r-new','pending',2,zeroblob(32))`)
	if _, err := tx.Exec(`INSERT INTO round_candidates VALUES ('c1','r-new','p1',0)`); err == nil {
		t.Fatal("consumed participant became a candidate")
	}
	execSQL(t, tx, `INSERT INTO round_candidates VALUES ('c1','r-new','p2',0)`)
	execSQL(t, tx, `INSERT INTO round_attempts(collection_id,round_id,attempt,failure_code,failed_at) VALUES ('c1','r-new',1,'InvalidState','2026-10-06T05:00:00Z')`)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rejectedSQL(t, store, `UPDATE round_attempts SET failure_code='StorageUnavailable' WHERE round_id='r-new'`)
	rejectedSQL(t, store, `UPDATE rounds SET active_operation_id='op1' WHERE id='r-new'`)
	assertNoHandles(t, store)
}

func TestAdmissionInputsAndOperationFingerprintRemainImmutableWhilePending(t *testing.T) {
	for name, query := range map[string]string{
		"round input":           `UPDATE rounds SET input_json='{}' WHERE id='r1'`,
		"operation generation":  `UPDATE operations SET creation_key=99 WHERE operation_id='op1'`,
		"operation fingerprint": `UPDATE operations SET public_fingerprint=zeroblob(32) WHERE operation_id='op1'`,
	} {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			seedRound(t, store, "c1", "r1", "op1", "executing")
			rejectedSQL(t, store, query)
		})
	}
}
func TestWinnerMustBelongToFrozenCandidatesBeforeAnyWinnerExists(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	execSQL(t, store.db, "INSERT INTO results VALUES ('c1','r1',?)", jsonValue(t, outcomeValue()))
	rejectedSQL(t, store, `INSERT INTO winners VALUES ('c1','r1','p3','gift',1)`)
	assertNoHandles(t, store)
}
func TestWinnerConsumptionUniqueAcrossRoundsEvenWithPreexistingCandidate(t *testing.T) {
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Construct the second candidate before inserting the first result; the
	// consumption UNIQUE must independently reject a second winner at commit.
	execSQL(t, tx, `UPDATE rounds SET state='completed' WHERE id='r1'`)
	execSQL(t, tx, `INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES ('c1','r-new',2,'executing',1,1,'{}','op-new')`)
	execSQL(t, tx, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('op-new','Rerun','c1','r-new','pending',1,zeroblob(32))`)
	execSQL(t, tx, `INSERT INTO round_candidates VALUES ('c1','r-new','p1',0)`)
	execSQL(t, tx, `INSERT INTO round_prizes VALUES ('c1','r-new','gift',0,1)`)
	execSQL(t, tx, `INSERT INTO results VALUES ('c1','r1','{}'),('c1','r-new','{}')`)
	execSQL(t, tx, `INSERT INTO winners VALUES ('c1','r1','p1','gift',1)`)
	if _, err := tx.Exec(`INSERT INTO winners VALUES ('c1','r-new','p1','gift',1)`); err == nil {
		t.Fatal("same participant consumed twice across rounds")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertNoHandles(t, store)
}
