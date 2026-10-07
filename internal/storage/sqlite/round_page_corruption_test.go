package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

func TestCollectionPageOutsidePageCorruptionRejectsNewestEmptyAndMissingAnchor(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"algorithm", `UPDATE results SET outcome_json=json_set(outcome_json,'$.AlgorithmVersion','') WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"executed-time", `UPDATE results SET outcome_json=json_set(outcome_json,'$.ExecutedAt','0001-01-01T00:00:00Z') WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"winner-not-candidate", `UPDATE results SET outcome_json=json_set(outcome_json,'$.Winners[0].ParticipantID','missing') WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"missing-winner-array", `UPDATE results SET outcome_json=json_set(outcome_json,'$.Winners',json('[]')) WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"SQL-winner-differs", `DROP TRIGGER immutable_winner; UPDATE winners SET participant_id='missing' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"SQL-winner-extra", `DROP TRIGGER valid_winner_slot; INSERT INTO winners SELECT collection_id,round_id,'extra',prize_id,2 FROM winners WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"SQL-winner-missing", `DROP TRIGGER undeletable_winner; DELETE FROM winners WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"SQL-winner-slot", `DROP TRIGGER immutable_winner; UPDATE winners SET slot=2 WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-pending", `UPDATE operations SET status='pending' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-failed", `UPDATE operations SET status='failed',failure_code='InvalidState' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-owner", `UPDATE operations SET collection_id='other' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-round", `UPDATE operations SET round_id='other' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"result-owner", `UPDATE results SET collection_id='other' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-kind", `UPDATE operations SET kind='SetSchedule' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"operation-future-revision", `UPDATE operations SET revision=9007199254740991 WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"state-result-mismatch", `UPDATE rounds SET state='executing' WHERE number=1`},
		{"claim-owner", `UPDATE rounds SET claim_json=json_set(claim_json,'$.Session','') WHERE number=1`},
		{"input-mode", `UPDATE rounds SET input_json=json_set(input_json,'$.Mode','unknown') WHERE number=1`},
		{"missing-result", `DROP TRIGGER undeletable_winner; DROP TRIGGER undeletable_result; DELETE FROM winners WHERE round_id=(SELECT id FROM rounds WHERE number=1); DELETE FROM results WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"null-input", `UPDATE rounds SET input_json='null' WHERE number=1`},
		{"malformed-input", `UPDATE rounds SET input_json='{' WHERE number=1`},
		{"trailing-input", `UPDATE rounds SET input_json=input_json||' {}' WHERE number=1`},
		{"unknown-input-key", `UPDATE rounds SET input_json=json_set(input_json,'$.Unknown',true) WHERE number=1`},
		{"null-outcome", `UPDATE results SET outcome_json='null' WHERE round_id=(SELECT id FROM rounds WHERE number=1)`},
		{"trailing-claim", `UPDATE rounds SET claim_json=claim_json||' {}' WHERE number=1`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, _, latest, entropy := collectionPageCompletedFixture(t)
			corruptionMode(t, store)
			execSQL(t, store.db, "PRAGMA foreign_keys=OFF")
			execSQL(t, store.db, test.sql)
			before := corruptionDurableSnapshot(t, store)
			for _, query := range []rl.CollectionPageQuery{{Limit: 1}, {Limit: 1, Offset: ^uint32(0)}, {Limit: 1, RoundID: "missing"}} {
				page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, query)
				requireEmptyCollectionPage(t, page, err)
				requireFault(t, err, contracts.InvalidState)
				assertNoHandles(t, store)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
				t.Fatal("hidden corruption changed durable bytes or RNG")
			}
		})
	}
}

func TestCollectionPageManualObjectRequiresBothIndependentBitsAndKeepsLegacyNull(t *testing.T) {
	for _, test := range []struct {
		name, object string
		valid        bool
	}{
		{"missing-both", "{}", false}, {"missing-manual", `{"OverrideExcluded":false}`, false},
		{"missing-override", `{"ManualIncluded":false}`, false}, {"null-manual", `{"ManualIncluded":null,"OverrideExcluded":false}`, false},
		{"null-override", `{"ManualIncluded":false,"OverrideExcluded":null}`, false},
		{"wrong-type", `{"ManualIncluded":0,"OverrideExcluded":false}`, false},
		{"unknown-key", `{"ManualIncluded":false,"OverrideExcluded":false,"Extra":true}`, false},
		{"legacy-null", "null", true}, {"both-false", `{"ManualIncluded":false,"OverrideExcluded":false}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, latest, _ := collectionPageCompletedFixture(t)
			corruptionMode(t, store)
			execSQL(t, store.db, fmt.Sprintf("UPDATE participants SET body_json=json_set(body_json,'$.Manual',json('%s')) WHERE id='p1'", test.object))
			before := corruptionDurableSnapshot(t, store)
			page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1})
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
				manual := page.Frozen.Participants[0].Manual
				if test.name == "legacy-null" && manual != nil || test.name == "both-false" && (manual == nil || manual.ManualIncluded || manual.OverrideExcluded) {
					t.Fatal("legacy unknown collapsed into explicit false")
				}
			} else {
				requireEmptyCollectionPage(t, page, err)
				requireFault(t, err, contracts.InvalidState)
			}
			if before != corruptionDurableSnapshot(t, store) {
				t.Fatal("manual read repaired corruption")
			}
			assertNoHandles(t, store)
		})
	}
}

func corruptCollectionPageAggregate(t *testing.T, store *Store, mode string) {
	t.Helper()
	corruptionMode(t, store)
	execSQL(t, store.db, "PRAGMA foreign_keys=OFF")
	switch mode {
	case "missing-participant":
		execSQL(t, store.db, `DROP TRIGGER undeletable_participant; DELETE FROM participants WHERE id='p1'; INSERT INTO participants(collection_id,id,position,included,body_json) SELECT collection_id,'replacement-selected',0,1,json_set(body_json,'$.ID','replacement-selected') FROM participants WHERE id='p2'`)
	case "consumed-exceeds-selected":
		execSQL(t, store.db, `UPDATE participants SET included=0,body_json=json_set(body_json,'$.Included',json('false'))`)
	case "duplicate-consumed":
		// A real test-only SQL view simulates external damage that the winner PK
		// ordinarily prevents. Each round remains independently valid, so only the
		// global consumed identity invariant distinguishes this corruption.
		execSQL(t, store.db, `UPDATE rounds SET input_json=json_set(input_json,'$.CandidateIDs',json('["p1","p2","p3"]')) WHERE number=2;
UPDATE results SET outcome_json=json_set(outcome_json,'$.Winners[0].ParticipantID','p1') WHERE round_id=(SELECT id FROM rounds WHERE number=2);
ALTER TABLE winners RENAME TO page_winner_rows;
CREATE VIEW winners AS SELECT collection_id,round_id,CASE WHEN round_id=(SELECT id FROM rounds WHERE number=2) THEN 'p1' ELSE participant_id END AS participant_id,prize_id,slot FROM page_winner_rows`)
	default:
		t.Fatal("unknown aggregate corruption")
	}
}
func TestCollectionPageAggregateWinnerPresenceUniquenessAndSelectedCountAreIndependent(t *testing.T) {
	for _, mode := range []string{"missing-participant", "duplicate-consumed", "consumed-exceeds-selected"} {
		t.Run(mode, func(t *testing.T) {
			store, _, latest, entropy := collectionPageCompletedFixture(t)
			corruptCollectionPageAggregate(t, store, mode)
			before := corruptionDurableSnapshot(t, store)
			for number := 1; number <= 3; number++ {
				var id contracts.RoundID
				if err := store.db.QueryRow("SELECT id FROM rounds WHERE number=?", number).Scan(&id); err != nil {
					t.Fatal(err)
				}
				if _, err := store.ReadRound(context.Background(), id); err != nil {
					t.Fatal("fixture did not isolate global invariant", number, err)
				}
			}
			page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1})
			requireEmptyCollectionPage(t, page, err)
			requireFault(t, err, contracts.InvalidState)
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
				t.Fatal("aggregate guard mutated DB or RNG")
			}
			assertNoHandles(t, store)
		})
	}
}
func TestCollectionPageLateSQLFailureKeepsPriorityOverEarlierAggregateCorruption(t *testing.T) {
	registerCorruptionReadProbe(t)
	store, _, latest, _ := collectionPageCompletedFixture(t)
	corruptCollectionPageAggregate(t, store, "missing-participant")
	execSQL(t, store.db, `ALTER TABLE rounds RENAME TO page_round_rows; CREATE VIEW rounds AS SELECT collection_id,id,number,state,version,attempt,jackpot_corruption_read_probe(input_json) AS input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM page_round_rows`)
	before := corruptionDurableSnapshot(t, store)
	probe := &corruptionReadProbe{at: 3, mode: "once"}
	activeCorruptionReadProbe.Store(probe)
	defer activeCorruptionReadProbe.Store(nil)
	page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1})
	activeCorruptionReadProbe.Store(nil)
	requireEmptyCollectionPage(t, page, err)
	var fault contracts.Fault
	if errors.As(err, &fault) || !strings.Contains(err.Error(), "injected actual SQLite read failure") || probe.calls.Load() != 3 {
		t.Fatal("aggregate validation hid later original SQL error", err, probe.calls.Load())
	}
	if before != corruptionDurableSnapshot(t, store) {
		t.Fatal("failed aggregate read changed durable state")
	}
	assertNoHandles(t, store)
}
func TestCollectionPageRoundMetadataBoundRejects100001BeforeDecodingAnyRound(t *testing.T) {
	store, _, latest, _ := collectionPageCancelledFixture(t, 1)
	execSQL(t, store.db, `ALTER TABLE rounds RENAME TO page_round_rows;
CREATE VIEW rounds AS WITH RECURSIVE numbers(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n<100001) SELECT r.collection_id,r.id||'-'||n AS id,n AS number,r.state,r.version,r.attempt,r.input_json,r.active_operation_id,r.scheduled_at,r.timezone,r.claim_json,r.failure_code FROM page_round_rows r CROSS JOIN numbers`)
	// Missing JSON tables would fail at txRound if the metadata bound were lost.
	execSQL(t, store.db, "DROP TRIGGER immutable_result; DROP TRIGGER undeletable_result; DROP TABLE results")
	page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 50})
	requireEmptyCollectionPage(t, page, err)
	requireFault(t, err, contracts.InvalidState)
	assertNoHandles(t, store)
	var count int
	if err = store.db.QueryRow("SELECT count(*) FROM page_round_rows").Scan(&count); err != nil || count != 1 {
		t.Fatal("metadata bound mutated original round", err)
	}
}

func TestCollectionPageNumberGapsAndShiftRejectNewestAndAnchoredReads(t *testing.T) {
	for _, mode := range []string{"deleted-middle", "shifted-all"} {
		t.Run(mode, func(t *testing.T) {
			store, _, latest, entropy := collectionPageCompletedFixture(t)
			corruptionMode(t, store)
			execSQL(t, store.db, "PRAGMA foreign_keys=OFF")
			if mode == "deleted-middle" {
				execSQL(t, store.db, "DROP TRIGGER undeletable_round; DELETE FROM rounds WHERE number=2")
			} else {
				// Descending updates avoid the ordinary UNIQUE(collection_id,number) guard;
				// every surviving individual round remains valid but no longer ordinal.
				for number := 3; number >= 1; number-- {
					execSQL(t, store.db, fmt.Sprintf("UPDATE rounds SET number=number+1 WHERE number=%d", number))
				}
			}
			if _, err := store.ReadRound(context.Background(), latest.ID); err != nil {
				t.Fatal("fixture did not isolate ordinal guard", err)
			}
			before := corruptionDurableSnapshot(t, store)
			for _, query := range []rl.CollectionPageQuery{{Limit: 1}, {Limit: 1, RoundID: latest.ID}, {Limit: 1, Offset: ^uint32(0)}} {
				page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, query)
				requireEmptyCollectionPage(t, page, err)
				requireFault(t, err, contracts.InvalidState)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
				t.Fatal("ordinal guard repaired DB or used RNG")
			}
			assertNoHandles(t, store)
		})
	}
}

type collectionPageOwnerProbe struct {
	calls         atomic.Int32
	metadataOwner contracts.CollectionID
}

var collectionPageOwnerRegistration sync.Once
var collectionPageOwnerRegistrationError error
var activeCollectionPageOwnerProbe atomic.Pointer[collectionPageOwnerProbe]

func registerCollectionPageOwnerProbe(t *testing.T) {
	t.Helper()
	collectionPageOwnerRegistration.Do(func() {
		collectionPageOwnerRegistrationError = driverSQLite.RegisterScalarFunction("jackpot_page_metadata_owner", 1, func(_ *driverSQLite.FunctionContext, args []driver.Value) (driver.Value, error) {
			probe := activeCollectionPageOwnerProbe.Load()
			if probe != nil && probe.calls.Add(1) == 1 {
				return string(probe.metadataOwner), nil
			}
			return args[0], nil
		})
	})
	if collectionPageOwnerRegistrationError != nil {
		t.Fatal(collectionPageOwnerRegistrationError)
	}
}
func TestCollectionPageForeignMetadataOwnerRejectsIndividuallyValidRound(t *testing.T) {
	registerCollectionPageOwnerProbe(t)
	store, service, latest, _ := collectionPageCancelledFixture(t, 1)
	request := productRequest(storageNow, rl.ReservationMode)
	request.Context.DraftID = "page-foreign-draft"
	request.Collection.SourceDraftID = request.Context.DraftID
	request.OperationID = "page-foreign-create"
	foreign, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "page-foreign-cancel", Context: productRoundContext(foreign, "session-one")})
	if err != nil {
		t.Fatal(err)
	}
	corruptionMode(t, store)
	execSQL(t, store.db, fmt.Sprintf("UPDATE rounds SET number=2 WHERE id='%s'", foreign.ID))
	// The real SQL scalar changes only the foreign row's metadata WHERE owner on
	// its first evaluation. The txRound join then observes its original owner.
	// This isolates the defensive page-owner assertion independently of ordinal,
	// claim, operation, and per-round outcome validation.
	execSQL(t, store.db, fmt.Sprintf(`ALTER TABLE rounds RENAME TO page_round_rows; CREATE VIEW rounds AS SELECT CASE WHEN id='%s' THEN jackpot_page_metadata_owner(collection_id) ELSE collection_id END AS collection_id,id,number,state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM page_round_rows`, foreign.ID))
	if round, err := store.ReadRound(context.Background(), foreign.ID); err != nil || round.CollectionID != foreign.CollectionID || round.Number != 2 {
		t.Fatal("foreign fixture individual validation", err)
	}
	before := corruptionDurableSnapshot(t, store)
	probe := &collectionPageOwnerProbe{metadataOwner: latest.CollectionID}
	activeCollectionPageOwnerProbe.Store(probe)
	defer activeCollectionPageOwnerProbe.Store(nil)
	page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 50})
	activeCollectionPageOwnerProbe.Store(nil)
	requireEmptyCollectionPage(t, page, err)
	requireFault(t, err, contracts.InvalidState)
	if probe.calls.Load() < 2 {
		t.Fatal("foreign metadata never reached record owner boundary")
	}
	if before != corruptionDurableSnapshot(t, store) {
		t.Fatal("page owner guard wrote state")
	}
	assertNoHandles(t, store)
}
