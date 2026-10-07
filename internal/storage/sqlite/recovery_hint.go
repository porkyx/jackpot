package sqlite

import "context"

// HasRecoveryWork is only a timer hint. It does not validate or recover rounds;
// startup, resume and every actual recovery keep their full durable preflight.
func (store *Store) HasRecoveryWork(ctx context.Context) (bool, error) {
	if err := writeContext(ctx); err != nil {
		return false, err
	}
	var present bool
	err := store.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM rounds WHERE state IN ('scheduled','executing'))").Scan(&present)
	return present, err
}
