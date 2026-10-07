package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

var _ rl.ReadStorage = (*Store)(nil)
var ErrNotFound = rl.ErrNotFound

func readContext(ctx context.Context, id string) error {
	if ctx == nil || id == "" {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return ctx.Err()
}
func decodeStored(raw string, target any) error {
	// Bound corrupt persisted input before allocating decoded object graphs.
	if len(raw) > 100<<20 || strings.TrimSpace(raw) == "null" {
		return contracts.NewFault(contracts.InvalidState)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return contracts.NewFault(contracts.InvalidState)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return contracts.NewFault(contracts.InvalidState)
	}
	return nil
}
func storedTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw.String)
	if err != nil {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	if _, err := contracts.NewResponseHeader("storage", value); err != nil {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	_, offset := value.Zone()
	if offset != 0 {
		return nil, contracts.NewFault(contracts.InvalidState)
	}
	return &value, nil
}

func (store *Store) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.OperationRecord{}, err
	}
	var record rl.OperationRecord
	var fingerprint []byte
	err := store.db.QueryRowContext(ctx, `SELECT operation_id,kind,collection_id,round_id,status,revision,public_fingerprint,failure_code FROM operations WHERE operation_id=?`, id).Scan(&record.Operation.ID, &record.Operation.Kind, &record.CollectionID, &record.RoundID, &record.Status, &record.Revision, &fingerprint, &record.FailureCode)
	if errors.Is(err, sql.ErrNoRows) {
		return rl.OperationRecord{Operation: rl.OperationIdentity{ID: id}, Status: contracts.OperationUnknown}, nil
	}
	if err != nil {
		return rl.OperationRecord{}, err
	}
	descriptor := contracts.OperationDescriptor{OperationID: record.Operation.ID, Kind: record.Operation.Kind, CollectionID: record.CollectionID, RoundID: record.RoundID, Status: contracts.OperationPending, Revision: record.Revision}
	if descriptor.ValidatePending() != nil || len(fingerprint) != 32 {
		return rl.OperationRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	switch record.Status {
	case contracts.OperationPending, contracts.OperationSucceeded:
		if record.FailureCode != "" {
			return rl.OperationRecord{}, contracts.NewFault(contracts.InvalidState)
		}
	case contracts.OperationFailed:
		if record.FailureCode.Validate() != nil {
			return rl.OperationRecord{}, contracts.NewFault(contracts.InvalidState)
		}
	default:
		return rl.OperationRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	copy(record.Operation.PublicFingerprint[:], fingerprint)
	return record, nil
}

func (store *Store) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.RoundRecord{}, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rl.RoundRecord{}, err
	}
	defer tx.Rollback()
	record, err := txRound(ctx, tx, id)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return rl.RoundRecord{}, err
	}
	return record, nil
}
func (store *Store) ReadCollection(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.FrozenCollection{}, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rl.FrozenCollection{}, err
	}
	defer tx.Rollback()
	var frozen string
	var source contracts.DraftID
	err = tx.QueryRowContext(ctx, "SELECT source_draft_id,frozen_json FROM collections WHERE id=?", id).Scan(&source, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		return rl.FrozenCollection{}, ErrNotFound
	}
	if err != nil {
		return rl.FrozenCollection{}, err
	}
	var result rl.FrozenCollection
	if err := decodeStored(frozen, &result); err != nil {
		return rl.FrozenCollection{}, err
	}
	if result.ID != id || result.SourceDraftID != source || result.Participants != nil {
		return rl.FrozenCollection{}, contracts.NewFault(contracts.InvalidState)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,included,body_json FROM participants WHERE collection_id=? ORDER BY position", id)
	if err != nil {
		return rl.FrozenCollection{}, err
	}
	defer rows.Close()
	result.Participants = make([]rl.ParticipantSnapshot, 0)
	for rows.Next() {
		if len(result.Participants) >= 100000 {
			return rl.FrozenCollection{}, contracts.NewFault(contracts.InvalidState)
		}
		var participant rl.ParticipantSnapshot
		var participantID contracts.ParticipantID
		var included bool
		var body string
		if err := rows.Scan(&participantID, &included, &body); err != nil {
			return rl.FrozenCollection{}, err
		}
		if err := decodeStored(body, &participant); err != nil {
			return rl.FrozenCollection{}, err
		}
		if participant.ID != participantID || participant.Included != included {
			return rl.FrozenCollection{}, contracts.NewFault(contracts.InvalidState)
		}
		result.Participants = append(result.Participants, participant)
	}
	if err := rows.Err(); err != nil {
		return rl.FrozenCollection{}, err
	}
	if err := rows.Close(); err != nil {
		return rl.FrozenCollection{}, err
	}
	if rl.ValidateBadgeSnapshot(result) != nil {
		return rl.FrozenCollection{}, contracts.NewFault(contracts.InvalidState)
	}
	if err := tx.Commit(); err != nil {
		return rl.FrozenCollection{}, err
	}
	return result, nil
}
