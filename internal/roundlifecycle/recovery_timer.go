package roundlifecycle

import (
	"context"

	"github.com/porkyx/jackpot/internal/contracts"
)

// RecoveryWorkReader is an optional durable timer hint. A reader without it
// retains the full Recover path. It is neither a cache nor mutation authority.
type RecoveryWorkReader interface {
	HasRecoveryWork(context.Context) (bool, error)
}

// RecoverTimer avoids full idle scans only for a timer signal. Explicit Recover,
// startup and OS resume still validate every persisted round before any change.
func (service *Service) RecoverTimer(ctx context.Context, request RecoverRequest) (RecoveryReport, error) {
	done, err := service.beginWork()
	if err != nil {
		return RecoveryReport{}, err
	}
	defer done()
	if err = contextError(ctx); err != nil {
		return RecoveryReport{}, err
	}
	if service.closed.Load() {
		return RecoveryReport{}, contracts.NewFault(contracts.InvalidState)
	}
	reader, ok := service.options.Storage.(RecoveryWorkReader)
	if !ok {
		return service.Recover(ctx, request)
	}
	if _, err = service.reader(); err != nil {
		return RecoveryReport{}, err
	}
	present, err := reader.HasRecoveryWork(ctx)
	if err != nil {
		return RecoveryReport{}, storageError(err)
	}
	if err = contextError(ctx); err != nil {
		return RecoveryReport{}, err
	}
	if service.closed.Load() {
		return RecoveryReport{}, contracts.NewFault(contracts.InvalidState)
	}
	if !present {
		return RecoveryReport{Completed: make([]contracts.RoundID, 0), Failed: make([]contracts.RoundID, 0), Due: make([]contracts.RoundID, 0)}, nil
	}
	return service.Recover(ctx, request)
}
