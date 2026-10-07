package sqlite

var migrationTwo = []string{
	`CREATE UNIQUE INDEX operation_collection_identity ON operations(collection_id,round_id,operation_id)`,
	`CREATE TABLE collections (
 id TEXT PRIMARY KEY CHECK(length(id)>0), source_draft_id TEXT NOT NULL UNIQUE CHECK(length(source_draft_id)>0),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 frozen_json TEXT NOT NULL CHECK(json_valid(frozen_json)),
 verifier_json TEXT NOT NULL CHECK(json_valid(verifier_json)), created_at TEXT NOT NULL
 ) STRICT`,
	`CREATE TRIGGER immutable_collection BEFORE UPDATE OF id,source_draft_id,frozen_json,verifier_json,created_at ON collections
 BEGIN SELECT RAISE(ABORT,'immutable collection snapshot'); END`,
	`CREATE TABLE participants (
 collection_id TEXT NOT NULL, id TEXT NOT NULL CHECK(length(id)>0), position INTEGER NOT NULL CHECK(position>=0),
 included INTEGER NOT NULL CHECK(included IN (0,1)), body_json TEXT NOT NULL CHECK(json_valid(body_json)),
 PRIMARY KEY(collection_id,id), UNIQUE(collection_id,position), FOREIGN KEY(collection_id) REFERENCES collections(id)
 ) STRICT`,
	`CREATE TRIGGER immutable_participant BEFORE UPDATE ON participants BEGIN SELECT RAISE(ABORT,'immutable participant'); END`,
	`CREATE TABLE rounds (
 collection_id TEXT NOT NULL, id TEXT NOT NULL UNIQUE CHECK(length(id)>0),
 number INTEGER NOT NULL CHECK(number BETWEEN 1 AND 4294967295),
 state TEXT NOT NULL CHECK(state IN ('pending_schedule','scheduled','executing','completed','cancelled','failed')),
 version INTEGER NOT NULL CHECK(version BETWEEN 1 AND 9007199254740991), attempt INTEGER NOT NULL CHECK(attempt BETWEEN 1 AND 4294967295),
 input_json TEXT NOT NULL CHECK(json_valid(input_json)), active_operation_id TEXT NOT NULL,
 scheduled_at TEXT, timezone TEXT NOT NULL DEFAULT '', claim_json TEXT CHECK(claim_json IS NULL OR json_valid(claim_json)),
 failure_code TEXT NOT NULL DEFAULT '', PRIMARY KEY(collection_id,id), UNIQUE(collection_id,number),
 FOREIGN KEY(collection_id) REFERENCES collections(id),
 FOREIGN KEY(collection_id,id,active_operation_id) REFERENCES operations(collection_id,round_id,operation_id) DEFERRABLE INITIALLY DEFERRED
 ) STRICT`,
	`CREATE UNIQUE INDEX active_round_gate ON rounds(collection_id) WHERE state IN ('pending_schedule','scheduled','executing','failed')`,
	`CREATE TRIGGER immutable_round_input BEFORE UPDATE OF collection_id,id,number,input_json ON rounds
 BEGIN SELECT RAISE(ABORT,'immutable round input'); END`,
	`CREATE TRIGGER immutable_terminal_round BEFORE UPDATE ON rounds WHEN OLD.state IN ('completed','cancelled')
 BEGIN SELECT RAISE(ABORT,'immutable terminal round'); END`,
	`CREATE TRIGGER valid_round_transition BEFORE UPDATE OF state ON rounds WHEN OLD.state!=NEW.state AND NOT (
 (OLD.state='pending_schedule' AND NEW.state IN ('scheduled','cancelled')) OR
 (OLD.state='scheduled' AND NEW.state IN ('executing','cancelled')) OR
 (OLD.state='executing' AND NEW.state IN ('completed','failed')) OR
 (OLD.state='failed' AND NEW.state='executing'))
 BEGIN SELECT RAISE(ABORT,'invalid round transition'); END`,
	`CREATE TABLE round_candidates (
 collection_id TEXT NOT NULL, round_id TEXT NOT NULL, participant_id TEXT NOT NULL, position INTEGER NOT NULL CHECK(position>=0),
 PRIMARY KEY(collection_id,round_id,participant_id), UNIQUE(collection_id,round_id,position),
 FOREIGN KEY(collection_id,round_id) REFERENCES rounds(collection_id,id),
 FOREIGN KEY(collection_id,participant_id) REFERENCES participants(collection_id,id)
 ) STRICT`,
	`CREATE TABLE round_prizes (
 collection_id TEXT NOT NULL, round_id TEXT NOT NULL, prize_id TEXT NOT NULL CHECK(length(prize_id)>0), position INTEGER NOT NULL CHECK(position>=0),
 requested_count INTEGER NOT NULL CHECK(requested_count BETWEEN 1 AND 10),
 PRIMARY KEY(collection_id,round_id,prize_id), UNIQUE(collection_id,round_id,position),
 FOREIGN KEY(collection_id,round_id) REFERENCES rounds(collection_id,id)
 ) STRICT`,
	`CREATE TABLE round_attempts (
 collection_id TEXT NOT NULL, round_id TEXT NOT NULL, attempt INTEGER NOT NULL CHECK(attempt BETWEEN 1 AND 4294967295),
 claim_json TEXT CHECK(claim_json IS NULL OR json_valid(claim_json)), failure_code TEXT NOT NULL DEFAULT '', failed_at TEXT,
 PRIMARY KEY(collection_id,round_id,attempt), FOREIGN KEY(collection_id,round_id) REFERENCES rounds(collection_id,id)
 ) STRICT`,
	`CREATE TABLE results (
 collection_id TEXT NOT NULL, round_id TEXT NOT NULL UNIQUE, outcome_json TEXT NOT NULL CHECK(json_valid(outcome_json)),
 PRIMARY KEY(collection_id,round_id), FOREIGN KEY(collection_id,round_id) REFERENCES rounds(collection_id,id)
 ) STRICT`,
	`CREATE TRIGGER immutable_result BEFORE UPDATE ON results BEGIN SELECT RAISE(ABORT,'immutable result'); END`,
	`CREATE TRIGGER undeletable_result BEFORE DELETE ON results BEGIN SELECT RAISE(ABORT,'immutable result'); END`,
	`CREATE TABLE winners (
 collection_id TEXT NOT NULL, round_id TEXT NOT NULL, participant_id TEXT NOT NULL, prize_id TEXT NOT NULL, slot INTEGER NOT NULL CHECK(slot BETWEEN 1 AND 10),
 PRIMARY KEY(collection_id,participant_id), UNIQUE(collection_id,round_id,prize_id,slot),
 FOREIGN KEY(collection_id,round_id) REFERENCES results(collection_id,round_id),
 FOREIGN KEY(collection_id,round_id,participant_id) REFERENCES round_candidates(collection_id,round_id,participant_id),
 FOREIGN KEY(collection_id,round_id,prize_id) REFERENCES round_prizes(collection_id,round_id,prize_id)
 ) STRICT`,
	`CREATE TRIGGER valid_winner_slot BEFORE INSERT ON winners WHEN NEW.slot >
 (SELECT requested_count FROM round_prizes WHERE collection_id=NEW.collection_id AND round_id=NEW.round_id AND prize_id=NEW.prize_id)
 BEGIN SELECT RAISE(ABORT,'invalid prize slot'); END`,
	`CREATE TRIGGER immutable_winner BEFORE UPDATE ON winners BEGIN SELECT RAISE(ABORT,'immutable winner'); END`,
	`CREATE TRIGGER undeletable_winner BEFORE DELETE ON winners BEGIN SELECT RAISE(ABORT,'immutable winner'); END`,
	// Rebuild without renaming the old table: renaming would rewrite the rounds
	// foreign key to the obsolete name. Keep creation keys and sequence high-water.
	`CREATE TEMP TABLE migration_sequence(value INTEGER NOT NULL)`,
	`INSERT INTO migration_sequence SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='operations'),0)`,
	`CREATE TABLE operations_next (
 creation_key INTEGER PRIMARY KEY AUTOINCREMENT,
 operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id)>0),
 kind TEXT NOT NULL CHECK(kind IN ('CreateCollection','Rerun','SetSchedule','CancelSchedule','RetryRound','ExecuteDue')),
 collection_id TEXT NOT NULL CHECK(length(collection_id)>0), round_id TEXT NOT NULL CHECK(length(round_id)>0),
 status TEXT NOT NULL CHECK(status IN ('pending','succeeded','failed')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 public_fingerprint BLOB NOT NULL CHECK(length(public_fingerprint)=32), failure_code TEXT NOT NULL DEFAULT '',
 UNIQUE(collection_id,round_id,operation_id),
 FOREIGN KEY(collection_id,round_id) REFERENCES rounds(collection_id,id) DEFERRABLE INITIALLY DEFERRED
 ) STRICT`,
	`INSERT INTO operations_next(creation_key,operation_id,kind,collection_id,round_id,status,revision,public_fingerprint)
 SELECT creation_key,operation_id,kind,collection_id,round_id,status,revision,public_fingerprint FROM operations`,
	`DROP TABLE operations`,
	`ALTER TABLE operations_next RENAME TO operations`,
	`UPDATE sqlite_sequence SET seq=MAX(seq,(SELECT value FROM migration_sequence)) WHERE name='operations'`,
	`INSERT INTO sqlite_sequence(name,seq) SELECT 'operations',value FROM migration_sequence WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='operations')`,
	`DROP TABLE migration_sequence`,
	`CREATE INDEX pending_operations_key ON operations(creation_key) WHERE status='pending'`,
	`CREATE TRIGGER immutable_operation_key BEFORE UPDATE OF creation_key,operation_id,kind,collection_id,round_id,public_fingerprint ON operations
 BEGIN SELECT RAISE(ABORT,'immutable operation identity'); END`,
	`CREATE TRIGGER immutable_terminal_operation BEFORE UPDATE ON operations WHEN OLD.status IN ('succeeded','failed')
 BEGIN SELECT RAISE(ABORT,'immutable terminal operation'); END`,
	`CREATE TRIGGER undeletable_operation BEFORE DELETE ON operations BEGIN SELECT RAISE(ABORT,'durable operation cannot be deleted'); END`,
	`CREATE TRIGGER immutable_candidate BEFORE UPDATE ON round_candidates BEGIN SELECT RAISE(ABORT,'immutable candidate'); END`,
	`CREATE TRIGGER eligible_candidate BEFORE INSERT ON round_candidates WHEN
 NOT EXISTS(SELECT 1 FROM participants WHERE collection_id=NEW.collection_id AND id=NEW.participant_id AND included=1) OR
 EXISTS(SELECT 1 FROM winners WHERE collection_id=NEW.collection_id AND participant_id=NEW.participant_id)
 BEGIN SELECT RAISE(ABORT,'ineligible candidate'); END`,
	`CREATE TRIGGER undeletable_candidate BEFORE DELETE ON round_candidates BEGIN SELECT RAISE(ABORT,'immutable candidate'); END`,
	`CREATE TRIGGER immutable_prize BEFORE UPDATE ON round_prizes BEGIN SELECT RAISE(ABORT,'immutable prize'); END`,
	`CREATE TRIGGER undeletable_prize BEFORE DELETE ON round_prizes BEGIN SELECT RAISE(ABORT,'immutable prize'); END`,
	`CREATE TRIGGER undeletable_round BEFORE DELETE ON rounds BEGIN SELECT RAISE(ABORT,'durable round cannot be deleted'); END`,
	`CREATE TRIGGER undeletable_participant BEFORE DELETE ON participants BEGIN SELECT RAISE(ABORT,'immutable participant'); END`,
	`CREATE TRIGGER undeletable_collection BEFORE DELETE ON collections BEGIN SELECT RAISE(ABORT,'durable collection cannot be deleted'); END`,
	`CREATE TRIGGER immutable_closed_attempt BEFORE UPDATE ON round_attempts WHEN OLD.failed_at IS NOT NULL
 BEGIN SELECT RAISE(ABORT,'immutable closed attempt'); END`,
	`CREATE TRIGGER undeletable_attempt BEFORE DELETE ON round_attempts BEGIN SELECT RAISE(ABORT,'durable attempt cannot be deleted'); END`,
}
