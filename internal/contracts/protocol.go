package contracts

import (
	"encoding/json"
	"time"
)

const ProtocolVersion uint32 = 1

type ResponseHeader struct {
	ProtocolVersion  uint32           `json:"protocolVersion"`
	BackendSessionID BackendSessionID `json:"backendSessionId"`
	OccurredAt       time.Time        `json:"occurredAt"`
}

func NewResponseHeader(session BackendSessionID, now time.Time) (ResponseHeader, error) {
	header := ResponseHeader{ProtocolVersion: ProtocolVersion, BackendSessionID: session, OccurredAt: now.UTC()}
	return header, header.Validate()
}

func (header ResponseHeader) Validate() error {
	if header.ProtocolVersion != ProtocolVersion {
		return NewFault(ProtocolError)
	}
	if err := ValidateID(header.BackendSessionID); err != nil {
		return err
	}
	if header.OccurredAt.IsZero() {
		return NewFault(InvalidInput)
	}
	if header.OccurredAt.Year() < 1 || header.OccurredAt.Year() > 9999 {
		return NewFault(InvalidInput)
	}
	_, offset := header.OccurredAt.Zone()
	if offset != 0 {
		return NewFault(InvalidInput)
	}
	return nil
}

// ExpectedRevision uses a pointer to distinguish required zero from absent/null input.
type MutationHeader struct {
	ProtocolVersion  uint32           `json:"protocolVersion"`
	BackendSessionID BackendSessionID `json:"backendSessionId"`
	OperationID      OperationID      `json:"operationId"`
	ExpectedRevision *Revision        `json:"expectedRevision"`
}

func (header MutationHeader) Validate() error {
	if header.ProtocolVersion != ProtocolVersion {
		return NewFault(ProtocolError)
	}
	if err := ValidateID(header.BackendSessionID); err != nil {
		return err
	}
	if err := ValidateID(header.OperationID); err != nil {
		return err
	}
	if header.ExpectedRevision == nil {
		return NewFault(InvalidInput)
	}
	return ValidateCounter(*header.ExpectedRevision)
}

type DraftMutationHeader struct {
	MutationHeader
	DraftID           DraftID            `json:"draftId"`
	ArticleGeneration *ArticleGeneration `json:"articleGeneration"`
}

func (header DraftMutationHeader) Validate() error {
	if err := header.MutationHeader.Validate(); err != nil {
		return err
	}
	if err := ValidateID(header.DraftID); err != nil {
		return err
	}
	if header.ArticleGeneration == nil {
		return NewFault(InvalidInput)
	}
	return ValidateCounter(*header.ArticleGeneration)
}

// Envelope validates its discriminant before serialization. Domain data uses concrete DTOs.
// Query responses omit operationId; mutation responses provide it.
type Envelope[T any] struct {
	ResponseHeader
	OK          bool         `json:"ok"`
	OperationID *OperationID `json:"operationId,omitempty"`
	Revision    *Revision    `json:"revision,omitempty"`
	Data        *T           `json:"data,omitempty"`
	Code        ErrorCode    `json:"code,omitempty"`
	MessageKey  string       `json:"messageKey,omitempty"`
}

func (envelope Envelope[T]) Validate() error {
	if err := envelope.ResponseHeader.Validate(); err != nil {
		return err
	}
	if envelope.OperationID != nil {
		if err := ValidateID(*envelope.OperationID); err != nil {
			return err
		}
	}
	if envelope.Revision != nil {
		if err := ValidateCounter(*envelope.Revision); err != nil {
			return err
		}
	}
	if envelope.OK {
		if envelope.Data == nil || envelope.Code != "" || envelope.MessageKey != "" {
			return NewFault(InvalidState)
		}
	} else {
		if envelope.Data != nil {
			return NewFault(InvalidState)
		}
		if err := envelope.Code.Validate(); err != nil {
			return err
		}
		if envelope.MessageKey != string(envelope.Code) {
			return NewFault(InvalidState)
		}
	}
	return nil
}

func (envelope Envelope[T]) MarshalJSON() ([]byte, error) {
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	type wire Envelope[T]
	return json.Marshal(wire(envelope))
}

type BootstrapData struct {
	BackendNow        time.Time             `json:"backendNow"`
	Theme             string                `json:"theme"`
	ActiveDraft       *DraftSummary         `json:"activeDraft"`
	PendingOperations []OperationDescriptor `json:"pendingOperations"`
	PendingCursor     *string               `json:"pendingCursor"`
	RecentResults     []ResultReference     `json:"recentResults"`
}

// A defined concrete DTO has no custom JSON marshaler. Wails can therefore
// generate its actual structure rather than degrading a json.Marshaler to any.
// The service validates it before returning it to the binding serializer.
type BootstrapResponse Envelope[BootstrapData]

func (response BootstrapResponse) Validate() error {
	return Envelope[BootstrapData](response).Validate()
}

type DraftSummary struct {
	DraftContext
	State        DraftState       `json:"state"`
	Snapshot     *SnapshotSummary `json:"snapshot"`
	CollectionID *CollectionID    `json:"collectionId"`
}

type ResultReference struct {
	CollectionID CollectionID `json:"collectionId"`
	RoundID      RoundID      `json:"roundId"`
	Revision     Revision     `json:"revision"`
}
