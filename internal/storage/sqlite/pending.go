package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"math"

	"github.com/porkyx/jackpot/internal/contracts"
)

var _ contracts.PendingOperationsReader = (*Store)(nil)

type pendingCursor struct{ after, through int64 }

func encodeCursor(cursor pendingCursor) string {
	var bytes [17]byte
	bytes[0] = 1
	binary.BigEndian.PutUint64(bytes[1:9], uint64(cursor.after))
	binary.BigEndian.PutUint64(bytes[9:17], uint64(cursor.through))
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}
func decodeCursor(raw string) (pendingCursor, error) {
	if len(raw) != 23 {
		return pendingCursor{}, contracts.NewFault(contracts.InvalidInput)
	}
	bytes, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(bytes) != 17 || bytes[0] != 1 {
		return pendingCursor{}, contracts.NewFault(contracts.InvalidInput)
	}
	after := binary.BigEndian.Uint64(bytes[1:9])
	through := binary.BigEndian.Uint64(bytes[9:17])
	if after < 1 || after > through || through > math.MaxInt64 {
		return pendingCursor{}, contracts.NewFault(contracts.InvalidInput)
	}
	return pendingCursor{after: int64(after), through: int64(through)}, nil
}

func (store *Store) ListPendingOperations(ctx context.Context, request contracts.PendingOperationsRequest) (contracts.PendingOperationsPage, error) {
	if ctx == nil {
		return contracts.PendingOperationsPage{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := request.Validate(); err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	cursor := pendingCursor{}
	if request.Cursor != nil {
		parsed, err := decodeCursor(*request.Cursor)
		if err != nil {
			return contracts.PendingOperationsPage{}, err
		}
		cursor = parsed
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	defer tx.Rollback()
	if request.Cursor == nil {
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(creation_key),0) FROM operations").Scan(&cursor.through); err != nil {
			return contracts.PendingOperationsPage{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT creation_key,operation_id,kind,collection_id,round_id,status,revision FROM operations
 WHERE status='pending' AND creation_key>? AND creation_key<=? ORDER BY creation_key LIMIT ?`, cursor.after, cursor.through, *request.Limit+1)
	if err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	defer rows.Close()
	page := contracts.PendingOperationsPage{Operations: make([]contracts.OperationDescriptor, 0, *request.Limit)}
	var lastKey int64
	for rows.Next() {
		var key int64
		var descriptor contracts.OperationDescriptor
		if err := rows.Scan(&key, &descriptor.OperationID, &descriptor.Kind, &descriptor.CollectionID, &descriptor.RoundID, &descriptor.Status, &descriptor.Revision); err != nil {
			return contracts.PendingOperationsPage{}, err
		}
		if key <= lastKey || key <= cursor.after || key > cursor.through {
			return contracts.PendingOperationsPage{}, contracts.NewFault(contracts.InvalidState)
		}
		if err := descriptor.ValidatePending(); err != nil {
			return contracts.PendingOperationsPage{}, err
		}
		if uint32(len(page.Operations)) == *request.Limit {
			next := encodeCursor(pendingCursor{after: lastKey, through: cursor.through})
			page.Cursor = &next
			break
		}
		page.Operations = append(page.Operations, descriptor)
		lastKey = key
	}
	if err := rows.Err(); err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	if err := rows.Close(); err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.PendingOperationsPage{}, err
	}
	return page, nil
}
