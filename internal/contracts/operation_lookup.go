package contracts

type OperationLookupRequest struct {
	OperationID OperationID `json:"operationId"`
}

func (request OperationLookupRequest) Validate() error { return ValidateID(request.OperationID) }

// Lookup is an observation of the stored operation. Unknown never means failed.
// It contains neither request payload, fingerprint nor credential material.
type OperationObservation struct {
	OperationID  OperationID    `json:"operationId"`
	State        OperationState `json:"state"`
	Kind         *string        `json:"kind"`
	CollectionID *CollectionID  `json:"collectionId"`
	RoundID      *RoundID       `json:"roundId"`
	Revision     *Revision      `json:"revision"`
	FailureCode  *ErrorCode     `json:"failureCode"`
}

func (observation OperationObservation) Validate() error {
	if err := ValidateID(observation.OperationID); err != nil {
		return err
	}
	if observation.State == OperationUnknown {
		if observation.Kind != nil || observation.CollectionID != nil || observation.RoundID != nil || observation.Revision != nil || observation.FailureCode != nil {
			return NewFault(InvalidState)
		}
		return nil
	}
	if observation.State != OperationPending && observation.State != OperationSucceeded && observation.State != OperationFailed {
		return NewFault(InvalidState)
	}
	if observation.Kind == nil || observation.CollectionID == nil || observation.RoundID == nil || observation.Revision == nil {
		return NewFault(InvalidState)
	}
	descriptor := OperationDescriptor{OperationID: observation.OperationID, Kind: *observation.Kind, CollectionID: *observation.CollectionID, RoundID: *observation.RoundID, Status: OperationPending, Revision: *observation.Revision}
	if err := descriptor.ValidatePending(); err != nil {
		return err
	}
	if observation.State == OperationFailed {
		if observation.FailureCode == nil {
			return NewFault(InvalidState)
		}
		return observation.FailureCode.Validate()
	}
	if observation.FailureCode != nil {
		return NewFault(InvalidState)
	}
	return nil
}

type OperationLookupResponse Envelope[OperationObservation]

func (response OperationLookupResponse) Validate() error {
	return Envelope[OperationObservation](response).Validate()
}
