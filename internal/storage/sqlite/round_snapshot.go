package sqlite

import (
	"context"
	"database/sql"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

// ReadCollectionSnapshot keeps frozen data and its revision in one committed
// snapshot. Every prior round is fully validated in this same transaction, but
// discarded individually instead of retaining unused candidate/outcome graphs.
func (store *Store) ReadCollectionSnapshot(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	defer tx.Rollback()
	collection, revision, err := txCollection(ctx, tx, id)
	if err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	if err = txValidateCollection(ctx, tx, id); err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	if err = tx.Commit(); err != nil {
		return rl.FrozenCollection{}, 0, err
	}
	return collection, revision, nil
}
