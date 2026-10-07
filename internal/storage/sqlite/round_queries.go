package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

var _ rl.Storage = (*Store)(nil)

func scheduleReplay(ctx context.Context, tx *sql.Tx, identity rl.OperationIdentity) (*rl.RoundRecord, error) {
	op, err := txOperation(ctx, tx, identity.ID)
	if err != nil {
		return nil, err
	}
	if op.Status == contracts.OperationUnknown {
		return nil, nil
	}
	if op.Operation.Kind != identity.Kind || op.Operation.PublicFingerprint != identity.PublicFingerprint {
		return nil, rl.ErrOperationConflict
	}
	round, err := txRound(ctx, tx, op.RoundID)
	return &round, err
}
func (store *Store) SetSchedule(ctx context.Context, request rl.ScheduleRequest) (rl.RoundRecord, error) {
	if err := writeContext(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	if request.Operation.ID == "" || request.Operation.Kind != "SetSchedule" || request.CollectionID == "" || request.RoundID == "" || request.Timezone != "Asia/Seoul" || request.AcceptedAt.IsZero() || request.ScheduledAt.IsZero() || request.ScheduledAt.Before(request.AcceptedAt.Add(10*time.Second)) {
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
	if replay, err := scheduleReplay(ctx, tx, request.Operation); err != nil {
		return rl.RoundRecord{}, err
	} else if replay != nil {
		if err = tx.Commit(); err != nil {
			return rl.RoundRecord{}, err
		}
		return *replay, nil
	}
	round, err := txRound(ctx, tx, request.RoundID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if round.CollectionID != request.CollectionID || round.Revision != request.ExpectedRevision || round.Version != request.ExpectedVersion {
		return rl.RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	if round.State != contracts.PendingSchedule {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	revision, err := nextRevision(ctx, tx, round.CollectionID, round.Revision)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	version, err := contracts.Increment(round.Version)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = insertOperation(ctx, tx, request.Operation, round.CollectionID, round.ID, contracts.OperationSucceeded, revision); err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET state='scheduled',version=?,scheduled_at=?,timezone=? WHERE id=? AND version=?", version, request.ScheduledAt.UTC().Format(time.RFC3339Nano), request.Timezone, round.ID, round.Version)); err != nil {
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
func (store *Store) CancelSchedule(ctx context.Context, request rl.CancelScheduleRequest) (rl.RoundRecord, error) {
	if err := writeContext(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	if request.Operation.ID == "" || request.Operation.Kind != "CancelSchedule" || request.CollectionID == "" || request.RoundID == "" || request.AcceptedAt.IsZero() {
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
	if replay, err := scheduleReplay(ctx, tx, request.Operation); err != nil {
		return rl.RoundRecord{}, err
	} else if replay != nil {
		if err = tx.Commit(); err != nil {
			return rl.RoundRecord{}, err
		}
		return *replay, nil
	}
	round, err := txRound(ctx, tx, request.RoundID)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if round.CollectionID != request.CollectionID || round.Revision != request.ExpectedRevision || round.Version != request.ExpectedVersion {
		return rl.RoundRecord{}, contracts.NewFault(contracts.StaleRevision)
	}
	if round.State != contracts.PendingSchedule && round.State != contracts.Scheduled {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	if round.State == contracts.Scheduled && (round.ScheduledAt == nil || !request.AcceptedAt.Before(round.ScheduledAt.Add(-10*time.Second))) {
		return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)
	}
	revision, err := nextRevision(ctx, tx, round.CollectionID, round.Revision)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	version, err := contracts.Increment(round.Version)
	if err != nil {
		return rl.RoundRecord{}, err
	}
	if err = insertOperation(ctx, tx, request.Operation, round.CollectionID, round.ID, contracts.OperationSucceeded, revision); err != nil {
		return rl.RoundRecord{}, err
	}
	if err = changed(tx.ExecContext(ctx, "UPDATE rounds SET state='cancelled',version=? WHERE id=? AND version=?", version, round.ID, round.Version)); err != nil {
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

func (store *Store) CollectionCount(ctx context.Context) (uint32, error) {
	if err := writeContext(ctx); err != nil {
		return 0, err
	}
	var count uint64
	err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM collections").Scan(&count)
	if err != nil {
		return 0, err
	}
	if count > uint64(^uint32(0)) {
		return 0, contracts.NewFault(contracts.InvalidState)
	}
	return uint32(count), nil
}
func (store *Store) FindCollectionByDraft(ctx context.Context, id contracts.DraftID) (contracts.CollectionID, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return "", err
	}
	var result contracts.CollectionID
	err := store.db.QueryRowContext(ctx, "SELECT id FROM collections WHERE source_draft_id=?", id).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", rl.ErrNotFound
	}
	return result, err
}
func roundIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]contracts.RoundID, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]contracts.RoundID, 0)
	for rows.Next() {
		if len(result) >= 100000 {
			return nil, contracts.NewFault(contracts.InvalidState)
		}
		var id contracts.RoundID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return result, nil
}
func readRounds(ctx context.Context, tx *sql.Tx, ids []contracts.RoundID) ([]rl.RoundRecord, error) {
	result := make([]rl.RoundRecord, 0, len(ids))
	for _, id := range ids {
		round, err := txRound(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, round)
	}
	return result, nil
}
func (store *Store) ListRounds(ctx context.Context, id contracts.CollectionID) ([]rl.RoundRecord, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var found string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM collections WHERE id=?", id).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return nil, rl.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	ids, err := roundIDs(ctx, tx, "SELECT id FROM rounds WHERE collection_id=? ORDER BY number", id)
	if err != nil {
		return nil, err
	}
	result, err := readRounds(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (store *Store) ListRecoverableRounds(ctx context.Context) ([]rl.RoundRecord, error) {
	if err := writeContext(ctx); err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Validate every durable round before any recovery mutation. IDs stream from
	// this snapshot; completed candidate arrays are discarded after each check.
	rows, err := tx.QueryContext(ctx, "SELECT id FROM rounds ORDER BY COALESCE(scheduled_at,''),id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]rl.RoundRecord, 0)
	count := 0
	for rows.Next() {
		if count >= 100000 {
			return nil, contracts.NewFault(contracts.InvalidState)
		}
		count++
		var id contracts.RoundID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		round, err := txRound(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if round.State == contracts.Executing || round.State == contracts.Scheduled {
			result = append(result, round)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (store *Store) ListCollections(ctx context.Context, offset, limit uint32) ([]rl.CollectionRecord, error) {
	if err := writeContext(ctx); err != nil {
		return nil, err
	}
	if limit == 0 || limit > 100 {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,source_draft_id,revision,frozen_json,created_at FROM collections ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]rl.CollectionRecord, 0)
	for rows.Next() {
		var record rl.CollectionRecord
		var frozen, created string
		if err = rows.Scan(&record.ID, &record.SourceDraftID, &record.Revision, &frozen, &created); err != nil {
			rows.Close()
			return nil, err
		}
		var snapshot rl.FrozenCollection
		if err = decodeStored(frozen, &snapshot); err != nil {
			rows.Close()
			return nil, err
		}
		record.GalleryName = snapshot.Article.GalleryName
		record.Title = snapshot.Article.Title
		record.URL = snapshot.Article.URL
		record.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return nil, contracts.NewFault(contracts.InvalidState)
		}
		result = append(result, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		ids, err := roundIDs(ctx, tx, "SELECT id FROM rounds WHERE collection_id=? ORDER BY number DESC LIMIT 1", result[index].ID)
		if err != nil {
			return nil, err
		}
		if len(ids) != 1 {
			return nil, contracts.NewFault(contracts.InvalidState)
		}
		round, err := txRound(ctx, tx, ids[0])
		if err != nil {
			return nil, err
		}
		result[index].LatestRound = &round
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func txCollection(ctx context.Context, tx *sql.Tx, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {
	var frozen string
	var source contracts.DraftID
	var revision contracts.Revision
	err := tx.QueryRowContext(ctx, "SELECT source_draft_id,revision,frozen_json FROM collections WHERE id=?", id).Scan(&source, &revision, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		return rl.FrozenCollection{}, 0, rl.ErrNotFound
	}
	if err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	var result rl.FrozenCollection
	if err = decodeStored(frozen, &result); err != nil {
		return result, 0, err
	}
	if result.ID != id || result.SourceDraftID != source || result.Participants != nil || revision == 0 || contracts.ValidateCounter(revision) != nil {
		return rl.FrozenCollection{}, 0, contracts.NewFault(contracts.InvalidState)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,included,body_json FROM participants WHERE collection_id=? ORDER BY position", id)
	if err != nil {
		return result, 0, err
	}
	defer rows.Close()
	result.Participants = make([]rl.ParticipantSnapshot, 0)
	for rows.Next() {
		if len(result.Participants) >= 100000 {
			return result, 0, contracts.NewFault(contracts.InvalidState)
		}
		var participant rl.ParticipantSnapshot
		var pid contracts.ParticipantID
		var included bool
		var raw string
		if err = rows.Scan(&pid, &included, &raw); err != nil {
			return result, 0, err
		}
		if err = decodeStored(raw, &participant); err != nil {
			return result, 0, err
		}
		if participant.ID != pid || participant.Included != included {
			return result, 0, contracts.NewFault(contracts.InvalidState)
		}
		result.Participants = append(result.Participants, participant)
	}
	if err = rows.Err(); err != nil {
		return result, 0, err
	}
	if err = rows.Close(); err != nil {
		return result, 0, err
	}
	if rl.ValidateBadgeSnapshot(result) != nil {
		return rl.FrozenCollection{}, 0, contracts.NewFault(contracts.InvalidState)
	}
	return result, revision, nil
}
func (store *Store) ReadCollectionState(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	defer tx.Rollback()
	collection, revision, err := txCollection(ctx, tx, id)
	if err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	ids, err := roundIDs(ctx, tx, "SELECT id FROM rounds WHERE collection_id=? ORDER BY number", id)
	if err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	rounds, err := readRounds(ctx, tx, ids)
	if err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return rl.FrozenCollection{}, nil, 0, err
	}
	return collection, rounds, revision, nil
}

func (store *Store) ReadActiveOperation(ctx context.Context, id contracts.RoundID) (rl.OperationRecord, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.OperationRecord{}, err
	}
	var operationID contracts.OperationID
	err := store.db.QueryRowContext(ctx, "SELECT active_operation_id FROM rounds WHERE id=?", id).Scan(&operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return rl.OperationRecord{}, rl.ErrNotFound
	}
	if err != nil {
		return rl.OperationRecord{}, err
	}
	return store.ReadOperation(ctx, operationID)
}

// Page and count share one committed read transaction. Metadata is streamed so
// Unicode-aware global search keeps memory bounded to the requested page.
func (store *Store) ListCollectionPage(ctx context.Context, offset, limit uint32, query, state string) ([]rl.CollectionRecord, uint32, error) {
	if err := writeContext(ctx); err != nil {
		return nil, 0, err
	}
	if limit == 0 || limit > 100 || len(query) > 4096 || !utf8.ValidString(query) {
		return nil, 0, contracts.NewFault(contracts.InvalidInput)
	}
	if state == "" {
		state = "all"
	}
	if state != "all" && contracts.RoundState(state).Validate() != nil {
		return nil, 0, contracts.NewFault(contracts.InvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT c.id,c.source_draft_id,c.revision,c.frozen_json,c.created_at,r.id FROM collections c JOIN rounds r ON r.collection_id=c.id AND r.number=(SELECT max(latest.number) FROM rounds latest WHERE latest.collection_id=c.id) WHERE (?='all' OR r.state=?) ORDER BY c.created_at DESC,c.id DESC", state, state)
	if err != nil {
		return nil, 0, err
	}
	page := make([]rl.CollectionRecord, 0, limit)
	rounds := make([]contracts.RoundID, 0, limit)
	total := uint32(0)
	needle := strings.ToLower(query)
	for rows.Next() {
		var record rl.CollectionRecord
		var frozen, created string
		var roundID contracts.RoundID
		if err = rows.Scan(&record.ID, &record.SourceDraftID, &record.Revision, &frozen, &created, &roundID); err != nil {
			rows.Close()
			return nil, 0, err
		}
		var snapshot rl.FrozenCollection
		if err = decodeStored(frozen, &snapshot); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if snapshot.ID != record.ID || snapshot.SourceDraftID != record.SourceDraftID || record.Revision == 0 || contracts.ValidateCounter(record.Revision) != nil {
			rows.Close()
			return nil, 0, contracts.NewFault(contracts.InvalidState)
		}
		if !strings.Contains(strings.ToLower(snapshot.Article.Title), needle) && !strings.Contains(strings.ToLower(snapshot.Article.GalleryName), needle) {
			continue
		}
		if total == ^uint32(0) {
			rows.Close()
			return nil, 0, contracts.NewFault(contracts.InvalidState)
		}
		total++
		if total <= offset || len(page) >= int(limit) {
			continue
		}
		record.Title = snapshot.Article.Title
		record.URL = snapshot.Article.URL
		record.GalleryName = snapshot.Article.GalleryName
		parsed, err := storedTime(sql.NullString{String: created, Valid: true})
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		record.CreatedAt = *parsed
		page = append(page, record)
		rounds = append(rounds, roundID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	if err = rows.Close(); err != nil {
		return nil, 0, err
	}
	for index, id := range rounds {
		round, err := txRound(ctx, tx, id)
		if err != nil {
			return nil, 0, err
		}
		if round.Revision != page[index].Revision {
			return nil, 0, contracts.NewFault(contracts.InvalidState)
		}
		// History displays round metadata; candidates and winners belong to the
		// dedicated collection/result query and are not retained by a history page.
		round.Input.CandidateIDs = nil
		if round.Outcome != nil {
			metadata := *round.Outcome
			metadata.Winners = nil
			round.Outcome = &metadata
		}
		round.Claim = nil
		page[index].LatestRound = &round
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	return page, total, nil
}
