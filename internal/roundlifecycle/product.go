package roundlifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/draw"
)

var (
	ErrNotFound          = errors.New("durable target not found")
	ErrOperationConflict = errors.New("OperationConflict")
	ErrAlreadyFinalized  = errors.New("AlreadyFinalized")
)

type ServiceOptions struct {
	Storage          Storage
	Session          contracts.BackendSessionID
	Entropy          draw.Entropy
	Clock            func() time.Time
	NewID            func() string
	Publish          func(context.Context, contracts.StateNotice) error
	ExecutionContext context.Context
	AppVersion       string
}

// Collection is constructed by Go's draft owner, never decoded from an IPC
// participant list. Prepare owns a detached public snapshot outside its mutex.
type CreateCollectionRequest struct {
	OperationID contracts.OperationID
	Context     contracts.DraftContext
	Collection  FrozenCollection
	Input       RoundInput
}

type RerunRequest struct {
	OperationID   contracts.OperationID
	Context       contracts.CollectionContext
	Prizes        []Prize
	Message, Mode string
}

type SetScheduleRequest struct {
	OperationID       contracts.OperationID
	Context           contracts.RoundContext
	ScheduledAt       time.Time
	QuickDelaySeconds *uint32
	Timezone          string
}

type CancelRequest struct {
	OperationID contracts.OperationID
	Context     contracts.RoundContext
}

type RetryRequest struct {
	OperationID contracts.OperationID
	Context     contracts.RoundContext
}

type ExecuteDueRequest struct{ RoundID contracts.RoundID }
type RecoverRequest struct{}
type RecoveryReport struct {
	Completed, Failed, Due []contracts.RoundID
}

type CollectionRecord struct {
	ID                      contracts.CollectionID
	SourceDraftID           contracts.DraftID
	Revision                contracts.Revision
	Title, URL, GalleryName string
	CreatedAt               time.Time
	LatestRound             *RoundRecord
}

type LifecycleReader interface {
	ListCollections(context.Context, uint32, uint32) ([]CollectionRecord, error)
	ListRounds(context.Context, contracts.CollectionID) ([]RoundRecord, error)
	ListRecoverableRounds(context.Context) ([]RoundRecord, error)
	FindCollectionByDraft(context.Context, contracts.DraftID) (contracts.CollectionID, error)
}
