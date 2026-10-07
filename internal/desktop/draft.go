package desktop

import (
	"context"
	"time"

	draftapp "github.com/porkyx/jackpot/internal/application"
	"github.com/porkyx/jackpot/internal/contracts"
)

// DraftService exposes concrete draft commands and bounded read projections.
// Snapshot promotion and cancellation belong to the Go application owner.
type DraftService struct {
	owner   *draftapp.DraftService
	session contracts.BackendSessionID
	now     func() time.Time
}

func NewDraftService(owner *draftapp.DraftService, session contracts.BackendSessionID, now func() time.Time) (*DraftService, error) {
	if owner == nil || session == "" || now == nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	return &DraftService{owner: owner, session: session, now: now}, nil
}
func (service *DraftService) header(ctx context.Context) (contracts.ResponseHeader, error) {
	if ctx == nil {
		return contracts.ResponseHeader{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.ResponseHeader{}, err
	}
	return contracts.NewResponseHeader(service.session, service.now())
}
func (service *DraftService) respond(header contracts.ResponseHeader, data contracts.DraftData, operation *contracts.OperationID, err error) (contracts.DraftResponse, error) {
	if err != nil {
		return contracts.DraftResponse(failureEnvelope[contracts.DraftData](header, err)), nil
	}
	revision := data.Summary.Revision
	response := contracts.DraftResponse{ResponseHeader: header, OK: true, Data: &data, OperationID: operation, Revision: &revision}
	if operation == nil {
		if err := response.Validate(); err != nil {
			return response, err
		}
		checked, checkErr := checkedQuery(contracts.Envelope[contracts.DraftData](response))
		return contracts.DraftResponse(checked), checkErr
	}
	return response, response.Validate()
}
func (service *DraftService) CreateDraft(ctx context.Context, request contracts.CreateDraftRequest) (contracts.DraftResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.DraftResponse{}, err
	}
	data, err := service.owner.Create(ctx, request)
	return service.respond(header, data, &request.OperationID, err)
}
func (service *DraftService) GetDraft(ctx context.Context, request contracts.DraftQuery) (contracts.DraftResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.DraftResponse{}, err
	}
	data, err := service.owner.Get(ctx, request)
	return service.respond(header, data, nil, err)
}
func (service *DraftService) LoadArticle(ctx context.Context, request contracts.LoadArticleRequest) (contracts.DraftResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.DraftResponse{}, err
	}
	data, err := service.owner.Load(ctx, request)
	return service.respond(header, data, &request.OperationID, err)
}
func (service *DraftService) EditDraft(ctx context.Context, request contracts.DraftEditRequest) (contracts.DraftResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.DraftResponse{}, err
	}
	data, err := service.owner.Edit(ctx, request)
	return service.respond(header, data, &request.OperationID, err)
}
func (service *DraftService) QueryParticipants(ctx context.Context, request contracts.ParticipantQuery) (contracts.ParticipantsResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.ParticipantsResponse{}, err
	}
	data, err := service.owner.Participants(ctx, request)
	if err != nil {
		return contracts.ParticipantsResponse(failureEnvelope[contracts.ParticipantsPage](header, err)), nil
	}
	response := contracts.ParticipantsResponse{ResponseHeader: header, OK: true, Data: &data}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.ParticipantsPage](response))
	return contracts.ParticipantsResponse(checked), checkErr
}
func (service *DraftService) QueryParticipantComments(ctx context.Context, request contracts.ParticipantCommentsQuery) (contracts.CommentsResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.CommentsResponse{}, err
	}
	data, err := service.owner.Comments(ctx, request)
	if err != nil {
		return contracts.CommentsResponse(failureEnvelope[contracts.CommentsPage](header, err)), nil
	}
	response := contracts.CommentsResponse{ResponseHeader: header, OK: true, Data: &data}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.CommentsPage](response))
	return contracts.CommentsResponse(checked), checkErr
}

func (service *DraftService) GetDraftOperation(ctx context.Context, request contracts.OperationLookupRequest) (contracts.DraftOperationResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.DraftOperationResponse{}, err
	}
	data, err := service.owner.Operation(ctx, request.OperationID)
	if err != nil {
		return contracts.DraftOperationResponse(failureEnvelope[contracts.DraftOperationData](header, err)), nil
	}
	response := contracts.DraftOperationResponse{ResponseHeader: header, OK: true, Data: &data}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.DraftOperationData](response))
	return contracts.DraftOperationResponse(checked), checkErr
}
