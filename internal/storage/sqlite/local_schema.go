package sqlite

// Version three removes obsolete password verifier storage while retaining every
// public frozen collection, participant, round, operation and committed result.
// Existing backup/transaction handling makes all three DDL changes atomic.
var migrationThree = []string{
	"DROP TRIGGER immutable_collection",
	"ALTER TABLE collections DROP COLUMN verifier_json",
	"CREATE TRIGGER immutable_collection BEFORE UPDATE OF id,source_draft_id,frozen_json,created_at ON collections BEGIN SELECT RAISE(ABORT,'immutable collection snapshot'); END",
}
