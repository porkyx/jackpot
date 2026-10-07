package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

// BootstrapSource projects the active Go draft and committed recent results.
// It contains no SQL or frontend state and is optional only in foundation harnesses.
type BootstrapSource interface {
	BootstrapProjection(context.Context) (*contracts.DraftSummary, []contracts.ResultReference, error)
}

// Service owns the bootstrap and durable recovery enumeration boundary.
type Service struct {
	session contracts.BackendSessionID
	now     func() time.Time
	pending contracts.PendingOperationsReader
	product BootstrapSource
}

func NewService(session contracts.BackendSessionID, now func() time.Time, pending contracts.PendingOperationsReader, sources ...BootstrapSource) (*Service, error) {
	if err := contracts.ValidateID(session); err != nil {
		return nil, err
	}
	if now == nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if nilDependency(pending) {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	var product BootstrapSource
	if len(sources) > 1 {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if len(sources) == 1 {
		if nilDependency(sources[0]) {
			return nil, contracts.NewFault(contracts.InvalidInput)
		}
		product = sources[0]
	}
	return &Service{session: session, now: now, pending: pending, product: product}, nil
}

func (service *Service) Bootstrap(ctx context.Context) (contracts.BootstrapResponse, error) {
	if ctx == nil {
		return contracts.BootstrapResponse{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.BootstrapResponse{}, err
	}
	header, err := contracts.NewResponseHeader(service.session, service.now())
	if err != nil {
		return contracts.BootstrapResponse{}, err
	}
	limit := uint32(64)
	page, err := service.pending.ListPendingOperations(ctx, contracts.PendingOperationsRequest{Limit: &limit})
	if err != nil {
		return contracts.BootstrapResponse(failureEnvelope[contracts.BootstrapData](header, err)), nil
	}
	if err := page.Validate(); err != nil {
		return contracts.BootstrapResponse(failureEnvelope[contracts.BootstrapData](header, err)), nil
	}
	data := contracts.BootstrapData{
		BackendNow: header.OccurredAt, Theme: "system",
		PendingOperations: page.Operations, PendingCursor: page.Cursor, RecentResults: []contracts.ResultReference{},
	}
	if service.product != nil {
		data.ActiveDraft, data.RecentResults, err = service.product.BootstrapProjection(ctx)
		if err != nil {
			return contracts.BootstrapResponse(failureEnvelope[contracts.BootstrapData](header, err)), nil
		}
		if data.RecentResults == nil || len(data.RecentResults) > 16 {
			return contracts.BootstrapResponse(failureEnvelope[contracts.BootstrapData](header, contracts.NewFault(contracts.InvalidState))), nil
		}
		if data.ActiveDraft != nil && data.ActiveDraft.BackendSessionID != service.session {
			return contracts.BootstrapResponse(failureEnvelope[contracts.BootstrapData](header, contracts.NewFault(contracts.InvalidState))), nil
		}
	}
	response := contracts.BootstrapResponse{ResponseHeader: header, OK: true, Data: &data}
	if err := response.Validate(); err != nil {
		return contracts.BootstrapResponse{}, err
	}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.BootstrapData](response))
	return contracts.BootstrapResponse(checked), checkErr
}

func (service *Service) ListPendingOperations(ctx context.Context, request contracts.PendingOperationsRequest) (contracts.PendingOperationsResponse, error) {
	if ctx == nil {
		return contracts.PendingOperationsResponse{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.PendingOperationsResponse{}, err
	}
	header, err := contracts.NewResponseHeader(service.session, service.now())
	if err != nil {
		return contracts.PendingOperationsResponse{}, err
	}
	if err := request.Validate(); err != nil {
		return contracts.PendingOperationsResponse(failureEnvelope[contracts.PendingOperationsPage](header, err)), nil
	}
	page, err := service.pending.ListPendingOperations(ctx, request)
	if err != nil {
		return contracts.PendingOperationsResponse(failureEnvelope[contracts.PendingOperationsPage](header, err)), nil
	}
	if err := page.Validate(); err != nil {
		return contracts.PendingOperationsResponse(failureEnvelope[contracts.PendingOperationsPage](header, err)), nil
	}
	response := contracts.PendingOperationsResponse{ResponseHeader: header, OK: true, Data: &page}
	if err := response.Validate(); err != nil {
		return contracts.PendingOperationsResponse{}, err
	}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.PendingOperationsPage](response))
	return contracts.PendingOperationsResponse(checked), checkErr
}

func failureEnvelope[T any](header contracts.ResponseHeader, err error) contracts.Envelope[T] {
	code := contracts.StorageUnavailable
	var fault contracts.Fault
	if errors.As(err, &fault) {
		if fault.Code.Validate() == nil {
			code = fault.Code
		} else {
			code = contracts.ProtocolError
		}
	}
	return contracts.Envelope[T]{ResponseHeader: header, OK: false, Code: code, MessageKey: string(code)}
}
