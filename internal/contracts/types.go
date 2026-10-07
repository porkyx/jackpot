// Package contracts defines values shared by domain modules and IPC adapters.
// The domain remains responsible for validating every externally supplied value.
package contracts

import "time"

type DraftID string
type CollectionID string
type RoundID string
type ParticipantID string
type OperationID string
type BackendSessionID string
type SnapshotID string

// SnapshotSummary is a small read model. Full comments stay behind paged queries.
// The collector owns completeness and the domain must reject incomplete snapshots at admission.
type SnapshotSummary struct {
	SnapshotID          SnapshotID `json:"snapshotId"`
	CollectedAt         time.Time  `json:"collectedAt"`
	Complete            bool       `json:"complete"`
	Pages               uint32     `json:"pages"`
	AcceptedComments    uint32     `json:"acceptedComments"`
	DeletedComments     uint32     `json:"deletedComments"`
	UnsupportedComments uint32     `json:"unsupportedComments"`
}

const MaxSafeInteger uint64 = 1<<53 - 1

type Revision uint64
type ArticleGeneration uint64
type RoundVersion uint64

type ErrorCode string

const (
	InvalidInput          ErrorCode = "InvalidInput"
	InvalidState          ErrorCode = "InvalidState"
	StaleRevision         ErrorCode = "StaleRevision"
	StaleArticleContext   ErrorCode = "StaleArticleContext"
	BackendSessionChanged ErrorCode = "BackendSessionChanged"
	ProtocolError         ErrorCode = "ProtocolError"
	StorageUnavailable    ErrorCode = "StorageUnavailable"
)

// Fault contains only safe public metadata. Raw input and credentials must never be included.
type Fault struct {
	Code       ErrorCode `json:"code"`
	MessageKey string    `json:"messageKey"`
}

func (f Fault) Error() string { return string(f.Code) }

func NewFault(code ErrorCode) error { return Fault{Code: code, MessageKey: string(code)} }

func (code ErrorCode) Validate() error {
	switch code {
	case InvalidInput, InvalidState, StaleRevision, StaleArticleContext, BackendSessionChanged, ProtocolError, StorageUnavailable:
		return nil
	default:
		return NewFault(InvalidState)
	}
}

func ValidateID[T ~string](id T) error {
	if id == "" {
		return NewFault(InvalidInput)
	}
	return nil
}
func ValidateCounter[T ~uint64](value T) error {
	if uint64(value) > MaxSafeInteger {
		return NewFault(InvalidInput)
	}
	return nil
}

func Increment[T ~uint64](value T) (T, error) {
	if uint64(value) >= MaxSafeInteger {
		return value, NewFault(InvalidInput)
	}
	return value + 1, nil
}

// DraftContext pins a command to one Go session, one draft snapshot and one revision.
type DraftContext struct {
	BackendSessionID  BackendSessionID  `json:"backendSessionId"`
	DraftID           DraftID           `json:"draftId"`
	Revision          Revision          `json:"revision"`
	ArticleGeneration ArticleGeneration `json:"articleGeneration"`
}

// CollectionContext uses the durable collection revision, never the draft revision.
type CollectionContext struct {
	BackendSessionID BackendSessionID `json:"backendSessionId"`
	CollectionID     CollectionID     `json:"collectionId"`
	Revision         Revision         `json:"revision"`
}

type RoundContext struct {
	CollectionContext
	RoundID RoundID      `json:"roundId"`
	Version RoundVersion `json:"roundVersion"`
}

func (context DraftContext) Validate() error {
	if err := ValidateID(context.BackendSessionID); err != nil {
		return err
	}
	if err := ValidateID(context.DraftID); err != nil {
		return err
	}
	if err := ValidateCounter(context.Revision); err != nil {
		return err
	}
	return ValidateCounter(context.ArticleGeneration)
}

func CheckDraftContext(current, expected DraftContext) error {
	if err := current.Validate(); err != nil {
		return NewFault(InvalidState)
	}
	if err := expected.Validate(); err != nil {
		return NewFault(InvalidInput)
	}
	if current.BackendSessionID != expected.BackendSessionID {
		return NewFault(BackendSessionChanged)
	}
	if current.DraftID != expected.DraftID {
		return NewFault(StaleArticleContext)
	}
	if current.ArticleGeneration != expected.ArticleGeneration {
		return NewFault(StaleArticleContext)
	}
	if current.Revision != expected.Revision {
		return NewFault(StaleRevision)
	}
	return nil
}

type RoundState string

const (
	PendingSchedule RoundState = "pending_schedule"
	Scheduled       RoundState = "scheduled"
	Executing       RoundState = "executing"
	Completed       RoundState = "completed"
	Cancelled       RoundState = "cancelled"
	Failed          RoundState = "failed"
)

func (state RoundState) Validate() error {
	switch state {
	case PendingSchedule, Scheduled, Executing, Completed, Cancelled, Failed:
		return nil
	default:
		return NewFault(InvalidState)
	}
}
func (state RoundState) OccupiesGate() (bool, error) {
	if err := state.Validate(); err != nil {
		return false, err
	}
	return state != Completed && state != Cancelled, nil
}
func CheckTransition(from, to RoundState) error {
	if err := from.Validate(); err != nil {
		return err
	}
	if err := to.Validate(); err != nil {
		return err
	}
	allowed := false
	switch from {
	case PendingSchedule:
		allowed = to == Scheduled || to == Cancelled
	case Scheduled:
		allowed = to == Executing || to == Cancelled
	case Executing:
		allowed = to == Completed || to == Failed
	case Failed:
		allowed = to == Executing
	case Completed, Cancelled:
	default:
		panic("validated round state became unreachable")
	}
	if !allowed {
		return NewFault(InvalidState)
	}
	return nil
}

func (state DraftState) Validate() error {
	switch state {
	case DraftEmpty, DraftLoading, DraftReady, DraftFinalized:
		return nil
	default:
		return NewFault(InvalidState)
	}
}
func (state OperationState) Validate() error {
	switch state {
	case OperationUnknown, OperationPending, OperationSucceeded, OperationFailed:
		return nil
	default:
		return NewFault(InvalidState)
	}
}

type DraftState string

const (
	DraftEmpty     DraftState = "empty"
	DraftLoading   DraftState = "loading"
	DraftReady     DraftState = "ready"
	DraftFinalized DraftState = "finalized"
)

type OperationState string

const (
	OperationUnknown   OperationState = "unknown"
	OperationPending   OperationState = "pending"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
)

type OperationDescriptor struct {
	OperationID  OperationID    `json:"operationId"`
	Kind         string         `json:"kind"`
	CollectionID CollectionID   `json:"collectionId"`
	RoundID      RoundID        `json:"roundId"`
	Status       OperationState `json:"status"`
	Revision     Revision       `json:"revision"`
}

func (descriptor OperationDescriptor) ValidatePending() error {
	if err := ValidateID(descriptor.OperationID); err != nil {
		return err
	}
	switch descriptor.Kind {
	case "CreateCollection", "Rerun", "SetSchedule", "CancelSchedule", "RetryRound", "ExecuteDue":
	default:
		return NewFault(InvalidState)
	}
	if err := ValidateID(descriptor.CollectionID); err != nil {
		return err
	}
	if err := ValidateID(descriptor.RoundID); err != nil {
		return err
	}
	if descriptor.Status != OperationPending {
		return NewFault(InvalidState)
	}
	return ValidateCounter(descriptor.Revision)
}
