package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func writeContext(ctx context.Context) error {
	if ctx == nil {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return ctx.Err()
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return contracts.NewFault(contracts.StaleRevision)
	}
	return nil
}
func txOperation(ctx context.Context, tx *sql.Tx, id contracts.OperationID) (rl.OperationRecord, error) {
	var record rl.OperationRecord
	var hash []byte
	err := tx.QueryRowContext(ctx, "SELECT operation_id,kind,collection_id,round_id,status,revision,public_fingerprint,failure_code FROM operations WHERE operation_id=?", id).Scan(&record.Operation.ID, &record.Operation.Kind, &record.CollectionID, &record.RoundID, &record.Status, &record.Revision, &hash, &record.FailureCode)
	if errors.Is(err, sql.ErrNoRows) {
		return rl.OperationRecord{Operation: rl.OperationIdentity{ID: id}, Status: contracts.OperationUnknown}, nil
	}
	if err != nil {
		return rl.OperationRecord{}, err
	}
	descriptor := contracts.OperationDescriptor{OperationID: record.Operation.ID, Kind: record.Operation.Kind, CollectionID: record.CollectionID, RoundID: record.RoundID, Status: contracts.OperationPending, Revision: record.Revision}
	if len(hash) != 32 || descriptor.ValidatePending() != nil || (record.Status != contracts.OperationPending && record.Status != contracts.OperationSucceeded && record.Status != contracts.OperationFailed) || (record.Status == contracts.OperationFailed && record.FailureCode.Validate() != nil) || (record.Status != contracts.OperationFailed && record.FailureCode != "") {
		return rl.OperationRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	copy(record.Operation.PublicFingerprint[:], hash)
	return record, nil
}
func txRound(ctx context.Context, tx *sql.Tx, id contracts.RoundID) (rl.RoundRecord, error) {
	var record rl.RoundRecord
	var input, frozenJSON string
	var activeOperation contracts.OperationID
	var schedule, claim, outcome, resultOwner sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT r.collection_id,r.id,r.number,r.attempt,r.state,r.version,c.revision,r.input_json,r.scheduled_at,r.timezone,r.claim_json,r.failure_code,o.outcome_json,r.active_operation_id,o.collection_id,c.frozen_json FROM rounds r JOIN collections c ON c.id=r.collection_id LEFT JOIN results o ON o.round_id=r.id WHERE r.id=?", id).Scan(&record.CollectionID, &record.ID, &record.Number, &record.Attempt, &record.State, &record.Version, &record.Revision, &input, &schedule, &record.Timezone, &claim, &record.FailureCode, &outcome, &activeOperation, &resultOwner, &frozenJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return record, rl.ErrNotFound
	}
	if err != nil {
		return record, err
	}
	if err := decodeStored(input, &record.Input); err != nil {
		return rl.RoundRecord{}, err
	}
	record.ScheduledAt, err = storedTime(schedule)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if claim.Valid {
		record.Claim = new(rl.ClaimToken)
		if err = decodeStored(claim.String, record.Claim); err != nil {
			return rl.RoundRecord{}, err
		}
	}
	if outcome.Valid {
		record.Outcome = new(rl.Outcome)
		if err = decodeStored(outcome.String, record.Outcome); err != nil {
			return rl.RoundRecord{}, err
		}
	}
	if outcome.Valid && (!resultOwner.Valid || resultOwner.String != string(record.CollectionID)) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if record.State.Validate() != nil || record.Revision == 0 || record.Version == 0 || contracts.ValidateCounter(record.Revision) != nil || contracts.ValidateCounter(record.Version) != nil || (record.State == contracts.Completed) != (record.Outcome != nil) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	// Healthy completed reads validate input and outcome once. On corruption,
	// retain the original early-input and late-outcome error priority so an
	// operation dependency failure is not hidden by a later outcome fault.
	var inputErr, outcomeErr error
	if record.Outcome == nil {
		inputErr = rl.ValidateRoundInput(record.Input)
	} else {
		outcomeErr = rl.ValidateOutcome(record.Input, *record.Outcome)
		if outcomeErr != nil {
			inputErr = rl.ValidateRoundInput(record.Input)
		}
	}
	if inputErr != nil || record.Number == 0 || record.Attempt == 0 || (record.State == contracts.Failed && record.FailureCode.Validate() != nil) || (record.State != contracts.Failed && record.FailureCode != "") || (record.State == contracts.Scheduled && (record.ScheduledAt == nil || record.Timezone != "Asia/Seoul")) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if record.Claim != nil && (record.Claim.CollectionID != record.CollectionID || record.Claim.RoundID != record.ID || record.Claim.OperationID != activeOperation || record.Claim.Session == "" || record.Claim.Attempt != record.Attempt || record.Claim.Version == 0 || record.Claim.Version > record.Version) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	// Active operation ownership and terminal status must agree even without an
	// outcome; an inconsistent executing record must never be repaired as stale.
	operation, err := txOperation(ctx, tx, activeOperation)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if operation.CollectionID != record.CollectionID || operation.RoundID != record.ID || operation.Revision == 0 || operation.Revision > record.Revision {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if operation.Operation.Kind != "CreateCollection" && operation.Operation.Kind != "Rerun" && operation.Operation.Kind != "RetryRound" && operation.Operation.Kind != "ExecuteDue" {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	expectedStatus := contracts.OperationSucceeded
	if record.State == contracts.Executing {
		expectedStatus = contracts.OperationPending
	}
	if record.State == contracts.Failed {
		expectedStatus = contracts.OperationFailed
	}
	if operation.Status != expectedStatus || (record.State == contracts.Failed && operation.FailureCode != record.FailureCode) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if record.Outcome != nil {
		if outcomeErr != nil {
			return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
		}
		rows, err := tx.QueryContext(ctx, "SELECT w.participant_id,w.prize_id,w.slot FROM winners w JOIN round_prizes p ON p.collection_id=w.collection_id AND p.round_id=w.round_id AND p.prize_id=w.prize_id WHERE w.round_id=? ORDER BY p.position,w.slot", record.ID)
		if err != nil {
			return rl.RoundRecord{}, err
		}
		index := 0
		for rows.Next() {
			var winner rl.Winner
			if err = rows.Scan(&winner.ParticipantID, &winner.PrizeID, &winner.Slot); err != nil {
				rows.Close()
				return rl.RoundRecord{}, err
			}
			if index >= len(record.Outcome.Winners) || winner != record.Outcome.Winners[index] {
				rows.Close()
				return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
			}
			index++
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return rl.RoundRecord{}, err
		}
		if err = rows.Close(); err != nil {
			return rl.RoundRecord{}, err
		}
		if index != len(record.Outcome.Winners) {
			return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
		}
	}
	var frozen rl.FrozenCollection
	if err := decodeStored(frozenJSON, &frozen); err != nil {
		return rl.RoundRecord{}, err
	}
	if record.Input.BadgeRules != nil || frozen.Filters.BadgeRules != nil {
		collection, _, err := txCollection(ctx, tx, record.CollectionID)
		if err != nil {
			return rl.RoundRecord{}, err
		}
		if rl.ValidateBadgeInput(collection, record.Input) != nil {
			return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
		}
	}
	return record, nil
}

// Existing collection writes validate every prior round in their write snapshot.
// A healthy latest round cannot hide a corrupt earlier result/operation.
func txValidateCollection(ctx context.Context, tx *sql.Tx, id contracts.CollectionID) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM rounds WHERE collection_id=? ORDER BY number", id)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count >= 100000 {
			return contracts.NewFault(contracts.InvalidState)
		}
		count++
		var roundID contracts.RoundID
		if err = rows.Scan(&roundID); err != nil {
			return err
		}
		if _, err = txRound(ctx, tx, roundID); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if count == 0 {
		return rl.ErrNotFound
	}
	return nil
}
func nextRevision(ctx context.Context, tx *sql.Tx, collection contracts.CollectionID, expected contracts.Revision) (contracts.Revision, error) {
	next, err := contracts.Increment(expected)
	if err != nil {
		return 0, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE collections SET revision=? WHERE id=? AND revision=?", next, collection, expected)); err != nil {
		return 0, err
	}
	return next, nil
}
func insertOperation(ctx context.Context, tx *sql.Tx, identity rl.OperationIdentity, collection contracts.CollectionID, round contracts.RoundID, status contracts.OperationState, revision contracts.Revision) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES(?,?,?,?,?,?,?)", identity.ID, identity.Kind, collection, round, status, revision, identity.PublicFingerprint[:])
	return err
}
func (store *Store) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {
	if err := writeContext(ctx); err != nil {
		return rl.Admission{}, err
	}
	if request.Operation.ID == "" || request.CollectionID == "" || request.RoundID == "" || request.Number == 0 || request.Attempt == 0 || request.AdmittedAt.IsZero() || (request.InitialState != contracts.Executing && request.InitialState != contracts.PendingSchedule) {
		return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	if _, err := contracts.NewResponseHeader("admission", request.AdmittedAt.UTC()); err != nil {
		return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	if request.Operation.Kind != "CreateCollection" && request.Operation.Kind != "Rerun" && request.Operation.Kind != "RetryRound" && request.Operation.Kind != "ExecuteDue" {
		return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	if (request.Operation.Kind == "CreateCollection") != (request.NewCollection != nil) || ((request.Operation.Kind == "RetryRound" || request.Operation.Kind == "ExecuteDue") && request.InitialState != contracts.Executing) {
		return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	if request.NewCollection != nil && ((request.Input.Mode == rl.ReservationMode) != (request.InitialState == contracts.PendingSchedule)) {
		return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := rl.ValidateRoundInput(request.Input); err != nil {
		return rl.Admission{}, err
	}
	if request.NewCollection != nil {
		if err := rl.ValidateBadgeInput(*request.NewCollection, request.Input); err != nil {
			return rl.Admission{}, err
		}
		if err := validateBadgeCandidateSet(*request.NewCollection, request.Input, nil); err != nil {
			return rl.Admission{}, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return rl.Admission{}, err
	}
	defer tx.Rollback()
	if request.NewCollection == nil {
		if err = txValidateCollection(ctx, tx, request.CollectionID); err != nil {
			return rl.Admission{}, err
		}
	}
	existing, err := txOperation(ctx, tx, request.Operation.ID)
	if err != nil {
		return rl.Admission{}, err
	}
	if existing.Status != contracts.OperationUnknown {
		if existing.Operation.Kind != request.Operation.Kind || existing.Operation.PublicFingerprint != request.Operation.PublicFingerprint {
			return rl.Admission{}, rl.ErrOperationConflict
		}
		round, err := txRound(ctx, tx, existing.RoundID)
		if err != nil {
			return rl.Admission{}, err
		}
		if err = tx.Commit(); err != nil {
			return rl.Admission{}, err
		}
		return rl.Admission{Operation: existing, Round: round, Replay: true}, nil
	}
	if request.NewCollection == nil {
		if err := txValidateBadgeAdmission(ctx, tx, request); err != nil {
			return rl.Admission{}, err
		}
	}
	revision := contracts.Revision(1)
	if request.NewCollection != nil {
		frozen := *request.NewCollection
		if frozen.ID != request.CollectionID || frozen.SourceDraftID == "" || frozen.FinalizedDraftRevision != request.ExpectedRevision {
			return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
		}
		var found contracts.CollectionID
		err = tx.QueryRowContext(ctx, "SELECT id FROM collections WHERE source_draft_id=?", frozen.SourceDraftID).Scan(&found)
		if err == nil {
			return rl.Admission{}, &rl.FinalizedError{CollectionID: found}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return rl.Admission{}, err
		}
		participants := frozen.Participants
		frozen.Participants = nil
		raw, err := json.Marshal(frozen)
		if err != nil {
			return rl.Admission{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO collections(id,source_draft_id,revision,frozen_json,created_at) VALUES(?,?,?,?,?)", frozen.ID, frozen.SourceDraftID, revision, string(raw), request.AdmittedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return rl.Admission{}, err
		}
		statement, err := tx.PrepareContext(ctx, "INSERT INTO participants(collection_id,id,position,included,body_json) VALUES(?,?,?,?,?)")
		if err != nil {
			return rl.Admission{}, err
		}
		for position, participant := range participants {
			raw, marshalErr := json.Marshal(participant)
			if marshalErr != nil {
				statement.Close()
				return rl.Admission{}, marshalErr
			}
			if _, err = statement.ExecContext(ctx, frozen.ID, participant.ID, position, participant.Included, string(raw)); err != nil {
				statement.Close()
				return rl.Admission{}, err
			}
		}
		if err = statement.Close(); err != nil {
			return rl.Admission{}, err
		}
	} else {
		revision, err = nextRevision(ctx, tx, request.CollectionID, request.ExpectedRevision)
		if err != nil {
			return rl.Admission{}, err
		}
	}
	status := contracts.OperationPending
	if request.InitialState == contracts.PendingSchedule {
		status = contracts.OperationSucceeded
	}
	if err = insertOperation(ctx, tx, request.Operation, request.CollectionID, request.RoundID, status, revision); err != nil {
		return rl.Admission{}, err
	}
	raw, err := json.Marshal(request.Input)
	if err != nil {
		return rl.Admission{}, err
	}
	version := contracts.RoundVersion(1)
	if request.Operation.Kind == "RetryRound" || request.Operation.Kind == "ExecuteDue" {
		before, err := txRound(ctx, tx, request.RoundID)
		if err != nil {
			return rl.Admission{}, err
		}
		if before.CollectionID != request.CollectionID || before.Version != request.ExpectedVersion || before.Outcome != nil {
			return rl.Admission{}, contracts.NewFault(contracts.StaleRevision)
		}
		if request.Operation.Kind == "RetryRound" {
			if before.State != contracts.Failed || request.Attempt != before.Attempt+1 || request.Attempt == 0 {
				return rl.Admission{}, contracts.NewFault(contracts.InvalidState)
			}
		} else if before.State != contracts.Scheduled || before.ScheduledAt == nil || request.AdmittedAt.Before(*before.ScheduledAt) || request.Attempt != before.Attempt {
			return rl.Admission{}, contracts.NewFault(contracts.InvalidState)
		}
		beforeRaw, _ := json.Marshal(before.Input)
		if string(beforeRaw) != string(raw) {
			return rl.Admission{}, contracts.NewFault(contracts.InvalidState)
		}
		version, err = contracts.Increment(before.Version)
		if err != nil {
			return rl.Admission{}, err
		}
		if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET state='executing',attempt=?,version=?,active_operation_id=?,claim_json=NULL,failure_code='' WHERE id=? AND version=?", request.Attempt, version, request.Operation.ID, request.RoundID, before.Version)); err != nil {
			return rl.Admission{}, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES(?,?,?,?,?,?,?,?)", request.CollectionID, request.RoundID, request.Number, request.InitialState, version, request.Attempt, string(raw), request.Operation.ID); err != nil {
			return rl.Admission{}, err
		}
		statement, err := tx.PrepareContext(ctx, "INSERT INTO round_candidates(collection_id,round_id,participant_id,position) VALUES(?,?,?,?)")
		if err != nil {
			return rl.Admission{}, err
		}
		for position, id := range request.Input.CandidateIDs {
			if _, err = statement.ExecContext(ctx, request.CollectionID, request.RoundID, id, position); err != nil {
				statement.Close()
				return rl.Admission{}, err
			}
		}
		if err = statement.Close(); err != nil {
			return rl.Admission{}, err
		}
		for position, prize := range request.Input.Prizes {
			if _, err = tx.ExecContext(ctx, "INSERT INTO round_prizes(collection_id,round_id,prize_id,position,requested_count) VALUES(?,?,?,?,?)", request.CollectionID, request.RoundID, prize.ID, position, prize.Count); err != nil {
				return rl.Admission{}, err
			}
		}
	}
	if request.Operation.Kind == "ExecuteDue" {
		if request.Session == "" {
			return rl.Admission{}, contracts.NewFault(contracts.InvalidInput)
		}
		claim := rl.ClaimToken{CollectionID: request.CollectionID, RoundID: request.RoundID, OperationID: request.Operation.ID, Session: request.Session, Attempt: request.Attempt, Version: version}
		claimJSON, err := json.Marshal(claim)
		if err != nil {
			return rl.Admission{}, err
		}
		if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET claim_json=? WHERE id=? AND version=? AND claim_json IS NULL", string(claimJSON), request.RoundID, version)); err != nil {
			return rl.Admission{}, err
		}
		if err = changed(tx.ExecContext(ctx, "UPDATE round_attempts SET claim_json=? WHERE round_id=? AND attempt=? AND claim_json IS NULL", string(claimJSON), request.RoundID, request.Attempt)); err != nil {
			return rl.Admission{}, err
		}
	}
	if request.Operation.Kind != "ExecuteDue" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO round_attempts(collection_id,round_id,attempt) VALUES(?,?,?)", request.CollectionID, request.RoundID, request.Attempt); err != nil {
			return rl.Admission{}, err
		}
	}
	round, err := txRound(ctx, tx, request.RoundID)
	if err != nil {
		return rl.Admission{}, err
	}
	operation, err := txOperation(ctx, tx, request.Operation.ID)
	if err != nil {
		return rl.Admission{}, err
	}
	if err = tx.Commit(); err != nil {
		return rl.Admission{}, err
	}
	return rl.Admission{Operation: operation, Round: round}, nil
}
func (store *Store) ClaimAttempt(ctx context.Context, request rl.ClaimRequest) (*rl.ClaimToken, error) {
	if err := writeContext(ctx); err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = txValidateCollection(ctx, tx, request.CollectionID); err != nil {
		return nil, err
	}
	round, err := txRound(ctx, tx, request.RoundID)
	if err != nil {
		return nil, err
	}
	if round.State != contracts.Executing || round.Claim != nil || round.CollectionID != request.CollectionID || round.Attempt != request.Attempt || round.Version != request.ExpectedVersion || round.Revision != request.ExpectedRevision {
		return nil, nil
	}
	operation, err := txOperation(ctx, tx, request.OperationID)
	if err != nil {
		return nil, err
	}
	if operation.Status != contracts.OperationPending || operation.RoundID != round.ID || operation.CollectionID != round.CollectionID || request.Session == "" {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	revision, err := nextRevision(ctx, tx, round.CollectionID, round.Revision)
	if err != nil {
		return nil, err
	}
	version, err := contracts.Increment(round.Version)
	if err != nil {
		return nil, err
	}
	claim := rl.ClaimToken{CollectionID: round.CollectionID, RoundID: round.ID, OperationID: request.OperationID, Session: request.Session, Attempt: round.Attempt, Version: version}
	raw, err := json.Marshal(claim)
	if err != nil {
		return nil, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET claim_json=?,version=? WHERE id=? AND version=? AND claim_json IS NULL AND active_operation_id=?", string(raw), version, round.ID, round.Version, request.OperationID)); err != nil {
		return nil, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE round_attempts SET claim_json=? WHERE round_id=? AND attempt=? AND claim_json IS NULL", string(raw), round.ID, round.Attempt)); err != nil {
		return nil, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE operations SET revision=? WHERE operation_id=? AND status='pending'", revision, request.OperationID)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &claim, nil
}
func (store *Store) CommitOutcome(ctx context.Context, request rl.CommitOutcomeRequest) (rl.RoundRecord, error) {
	if err := writeContext(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	defer tx.Rollback()
	if err = txValidateCollection(ctx, tx, request.Claim.CollectionID); err != nil {
		return rl.RoundRecord{}, err
	}
	round, err := txRound(ctx, tx, request.Claim.RoundID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if round.Claim == nil || *round.Claim != request.Claim || round.State != contracts.Executing || round.Version != request.Claim.Version || round.Revision != request.ExpectedRevision {
		return rl.RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	if err = rl.ValidateOutcome(round.Input, request.Outcome); err != nil {
		return rl.RoundRecord{}, err
	}
	revision, err := nextRevision(ctx, tx, round.CollectionID, round.Revision)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	version, err := contracts.Increment(round.Version)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	raw, err := json.Marshal(request.Outcome)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO results(collection_id,round_id,outcome_json) VALUES(?,?,?)", round.CollectionID, round.ID, string(raw)); err != nil {
		return rl.RoundRecord{}, err
	}
	for _, winner := range request.Outcome.Winners {
		if _, err = tx.ExecContext(ctx, "INSERT INTO winners(collection_id,round_id,participant_id,prize_id,slot) VALUES(?,?,?,?,?)", round.CollectionID, round.ID, winner.ParticipantID, winner.PrizeID, winner.Slot); err != nil {
			return rl.RoundRecord{}, err
		}
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET state='completed',version=? WHERE id=? AND version=?", version, round.ID, round.Version)); err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE operations SET status='succeeded',revision=? WHERE operation_id=? AND status='pending'", revision, request.Claim.OperationID)); err != nil {
		return rl.RoundRecord{}, err
	}
	result, err := txRound(ctx, tx, round.ID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return rl.RoundRecord{}, err
	}
	return result, nil
}
func (store *Store) RecordAttemptFailure(ctx context.Context, request rl.AttemptFailureRequest) (rl.RoundRecord, error) {
	if err := writeContext(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	if request.Code.Validate() != nil || request.FailedAt.IsZero() {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	defer tx.Rollback()
	if err = txValidateCollection(ctx, tx, request.CollectionID); err != nil {
		return rl.RoundRecord{}, err
	}
	round, err := txRound(ctx, tx, request.RoundID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if round.CollectionID != request.CollectionID || round.State != contracts.Executing || round.Outcome != nil || round.Attempt != request.Attempt || round.Version != request.ExpectedVersion || round.Revision != request.ExpectedRevision {
		return rl.RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	var activeOperation contracts.OperationID
	if err = tx.QueryRowContext(ctx, "SELECT active_operation_id FROM rounds WHERE id=?", round.ID).Scan(&activeOperation); err != nil {
		return rl.RoundRecord{}, err
	}
	if request.OperationID != activeOperation {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if request.Claim != nil && (round.Claim == nil || *request.Claim != *round.Claim) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	revision, err := nextRevision(ctx, tx, round.CollectionID, round.Revision)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	version, err := contracts.Increment(round.Version)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET state='failed',version=?,failure_code=? WHERE id=? AND version=?", version, request.Code, round.ID, round.Version)); err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE round_attempts SET failure_code=?,failed_at=? WHERE round_id=? AND attempt=? AND failed_at IS NULL", request.Code, request.FailedAt.UTC().Format(time.RFC3339Nano), round.ID, round.Attempt)); err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE operations SET status='failed',revision=?,failure_code=? WHERE operation_id=? AND status='pending'", revision, request.Code, request.OperationID)); err != nil {
		return rl.RoundRecord{}, err
	}
	result, err := txRound(ctx, tx, round.ID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return rl.RoundRecord{}, err
	}
	return result, nil
}
