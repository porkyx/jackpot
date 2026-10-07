// Package roundlifecycle owns admission, execution and committed recovery.
// These concrete storage contracts are implemented by the SQL adapter in the
// owning draw/scheduler tickets; no SQL transaction callback crosses this seam.
package roundlifecycle

import (
	"context"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

type PrizeID string
type Prize struct {
	ID    PrizeID
	Name  string
	Count uint32
}
type CommentSnapshot struct {
	ID        string
	ParentID  *string
	Kind      string
	Text      string
	PostedAt  *time.Time
	MediaURLs []string
}

// ManualStateSnapshot records both independent selection overrides.
// A nil ParticipantSnapshot.Manual means an older snapshot did not record them.
type ManualStateSnapshot struct {
	ManualIncluded   bool
	OverrideExcluded bool
}
type ParticipantSnapshot struct {
	BadgeCategory                    contracts.BadgeCategory `json:",omitempty"`
	ID                               contracts.ParticipantID
	Nickname, PublicIdentifier, Kind string
	Included                         bool
	Manual                           *ManualStateSnapshot `json:",omitempty"`
	Classification, Reason           string
	Comments                         []CommentSnapshot
}
type ArticleSnapshot struct {
	URL, Title, GalleryID, GalleryName, GalleryKind, Number string
	AuthorNickname                                          string
	AuthorIdentifier                                        *string
	PostedAt                                                *time.Time
}
type FilterSnapshot struct {
	BadgeRules                                        *contracts.BadgeRules `json:",omitempty"`
	ExcludeAnonymous, ExcludeAuthor, ExcludeDcconOnly bool
	TimeCut                                           *time.Time
	IncludeKeywords, ExcludeKeywords                  []string
	RulesVersion                                      string
}
type FrozenCollection struct {
	ID                     contracts.CollectionID
	SourceDraftID          contracts.DraftID
	FinalizedDraftRevision contracts.Revision
	ArticleGeneration      contracts.ArticleGeneration
	Snapshot               contracts.SnapshotSummary
	Article                ArticleSnapshot
	Participants           []ParticipantSnapshot
	Filters                FilterSnapshot
}
type RoundInput struct {
	BadgeRules          *contracts.BadgeRules     `json:",omitempty"`
	CandidateCategories []contracts.BadgeCategory `json:",omitempty"`
	CandidateIDs        []contracts.ParticipantID
	Prizes              []Prize
	Message             string
	Mode                string
}
type OperationIdentity struct {
	ID   contracts.OperationID
	Kind string
	// Hash only canonical public payload; never password, verifier or authority.
	PublicFingerprint [32]byte
}
type AdmitRoundRequest struct {
	Operation        OperationIdentity
	CollectionID     contracts.CollectionID
	ExpectedRevision contracts.Revision
	ExpectedVersion  contracts.RoundVersion
	NewCollection    *FrozenCollection
	RoundID          contracts.RoundID
	Number, Attempt  uint32
	Input            RoundInput
	InitialState     contracts.RoundState
	AdmittedAt       time.Time
	Session          contracts.BackendSessionID
}
type OperationRecord struct {
	Operation    OperationIdentity
	Status       contracts.OperationState
	CollectionID contracts.CollectionID
	RoundID      contracts.RoundID
	Revision     contracts.Revision
	FailureCode  contracts.ErrorCode
}
type RoundRecord struct {
	CollectionID    contracts.CollectionID
	ID              contracts.RoundID
	Number, Attempt uint32
	State           contracts.RoundState
	Revision        contracts.Revision
	Version         contracts.RoundVersion
	Input           RoundInput
	ScheduledAt     *time.Time
	Timezone        string
	Claim           *ClaimToken
	Outcome         *Outcome
	FailureCode     contracts.ErrorCode
}
type Admission struct {
	Operation OperationRecord
	Round     RoundRecord
	Replay    bool
}
type ClaimRequest struct {
	CollectionID     contracts.CollectionID
	RoundID          contracts.RoundID
	OperationID      contracts.OperationID
	ExpectedRevision contracts.Revision
	ExpectedVersion  contracts.RoundVersion
	Attempt          uint32
	// Constructed by Go; never accepted as a frontend system-actor credential.
	Session contracts.BackendSessionID
}
type ClaimToken struct {
	CollectionID contracts.CollectionID
	RoundID      contracts.RoundID
	OperationID  contracts.OperationID
	Session      contracts.BackendSessionID
	Attempt      uint32
	Version      contracts.RoundVersion
}
type Winner struct {
	ParticipantID contracts.ParticipantID
	PrizeID       PrizeID
	Slot          uint32
}
type Outcome struct {
	Winners                      []Winner
	ExecutedAt                   time.Time
	AlgorithmVersion, AppVersion string
}
type CommitOutcomeRequest struct {
	Claim            ClaimToken
	ExpectedRevision contracts.Revision
	Outcome          Outcome
}
type AttemptFailureRequest struct {
	CollectionID     contracts.CollectionID
	RoundID          contracts.RoundID
	OperationID      contracts.OperationID
	ExpectedVersion  contracts.RoundVersion
	Attempt          uint32
	Claim            *ClaimToken
	ExpectedRevision contracts.Revision
	Code             contracts.ErrorCode
	FailedAt         time.Time
}
type ScheduleRequest struct {
	Operation               OperationIdentity
	CollectionID            contracts.CollectionID
	RoundID                 contracts.RoundID
	ExpectedRevision        contracts.Revision
	ExpectedVersion         contracts.RoundVersion
	AcceptedAt, ScheduledAt time.Time
	Timezone                string
}
type CancelScheduleRequest struct {
	Operation        OperationIdentity
	CollectionID     contracts.CollectionID
	RoundID          contracts.RoundID
	ExpectedRevision contracts.Revision
	ExpectedVersion  contracts.RoundVersion
	AcceptedAt       time.Time
}

// Nil claim with nil error means another owner won; it must consume no entropy.
// Committed reads return no computed-but-uncommitted outcome. Every write below
// includes operation + related collection/round changes in one transaction.
type WriteStorage interface {
	AdmitRound(context.Context, AdmitRoundRequest) (Admission, error)
	ClaimAttempt(context.Context, ClaimRequest) (*ClaimToken, error)
	CommitOutcome(context.Context, CommitOutcomeRequest) (RoundRecord, error)
	RecordAttemptFailure(context.Context, AttemptFailureRequest) (RoundRecord, error)
	SetSchedule(context.Context, ScheduleRequest) (RoundRecord, error)
	CancelSchedule(context.Context, CancelScheduleRequest) (RoundRecord, error)
}
type ReadStorage interface {
	ReadOperation(context.Context, contracts.OperationID) (OperationRecord, error)
	ReadRound(context.Context, contracts.RoundID) (RoundRecord, error)
	ReadCollection(context.Context, contracts.CollectionID) (FrozenCollection, error)
}
type Storage interface {
	ReadStorage
	WriteStorage
}
