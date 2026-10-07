package contracts

const StateChangedEvent = "jackpot:state-changed"

type EventEntityKind string

const (
	DraftEntity      EventEntityKind = "draft"
	CollectionEntity EventEntityKind = "collection"
)

// StateNotice is a committed-state hint, never a result or a secret payload.
// The consumer reconciles through reads; notification delivery is not a commit.
type StateNotice struct {
	BackendSessionID BackendSessionID `json:"backendSessionId"`
	EntityKind       EventEntityKind  `json:"entityKind"`
	EntityID         string           `json:"entityId"`
	Revision         Revision         `json:"revision"`
	OperationID      *OperationID     `json:"operationId"`
}

func (notice StateNotice) Validate() error {
	if err := ValidateID(notice.BackendSessionID); err != nil {
		return err
	}
	if err := ValidateID(notice.EntityID); err != nil {
		return err
	}
	switch notice.EntityKind {
	case DraftEntity, CollectionEntity:
	default:
		return NewFault(InvalidInput)
	}
	if err := ValidateCounter(notice.Revision); err != nil {
		return err
	}
	if notice.OperationID != nil {
		if err := ValidateID(*notice.OperationID); err != nil {
			return err
		}
	}
	return nil
}
