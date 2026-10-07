package desktop

import (
	"context"
	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"time"
)

type OperationReader interface {
	ReadOperation(context.Context, contracts.OperationID) (rl.OperationRecord, error)
}
type RecoveryService struct {
	session contracts.BackendSessionID
	now     func() time.Time
	reader  OperationReader
}

func NewRecoveryService(session contracts.BackendSessionID, now func() time.Time, reader OperationReader) (*RecoveryService, error) {
	if err := contracts.ValidateID(session); err != nil {
		return nil, err
	}
	if now == nil || nilDependency(reader) {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	return &RecoveryService{session: session, now: now, reader: reader}, nil
}
func (service *RecoveryService) GetOperation(ctx context.Context, request contracts.OperationLookupRequest) (contracts.OperationLookupResponse, error) {
	if ctx == nil {
		return contracts.OperationLookupResponse{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.OperationLookupResponse{}, err
	}
	header, err := contracts.NewResponseHeader(service.session, service.now())
	if err != nil {
		return contracts.OperationLookupResponse{}, err
	}
	if err = request.Validate(); err != nil {
		return contracts.OperationLookupResponse(failureEnvelope[contracts.OperationObservation](header, err)), nil
	}
	record, err := service.reader.ReadOperation(ctx, request.OperationID)
	if err != nil {
		return contracts.OperationLookupResponse(failureEnvelope[contracts.OperationObservation](header, err)), nil
	}
	if record.Status == contracts.OperationUnknown && (record.Operation.Kind != "" || record.CollectionID != "" || record.RoundID != "" || record.Revision != 0 || record.FailureCode != "" || record.Operation.PublicFingerprint != [32]byte{}) {
		return contracts.OperationLookupResponse(failureEnvelope[contracts.OperationObservation](header, contracts.NewFault(contracts.InvalidState))), nil
	}
	observation := contracts.OperationObservation{OperationID: record.Operation.ID, State: record.Status}
	if record.Status != contracts.OperationUnknown {
		kind, collection, round, revision := record.Operation.Kind, record.CollectionID, record.RoundID, record.Revision
		observation.Kind = &kind
		observation.CollectionID = &collection
		observation.RoundID = &round
		observation.Revision = &revision
		if record.FailureCode != "" {
			code := record.FailureCode
			observation.FailureCode = &code
		}
	}
	if observation.OperationID != request.OperationID || observation.Validate() != nil {
		return contracts.OperationLookupResponse(failureEnvelope[contracts.OperationObservation](header, contracts.NewFault(contracts.InvalidState))), nil
	}
	response := contracts.OperationLookupResponse{ResponseHeader: header, OK: true, Data: &observation}
	if err = response.Validate(); err != nil {
		return contracts.OperationLookupResponse{}, err
	}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.OperationObservation](response))
	return contracts.OperationLookupResponse(checked), checkErr
}
