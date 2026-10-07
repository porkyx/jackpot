package contracts

import "context"

type PendingOperationsRequest struct {
	Cursor *string `json:"cursor"`
	Limit  *uint32 `json:"limit"`
}

func (request PendingOperationsRequest) Validate() error {
	if request.Limit == nil || *request.Limit < 1 || *request.Limit > 64 {
		return NewFault(InvalidInput)
	}
	if request.Cursor != nil && *request.Cursor == "" {
		return NewFault(InvalidInput)
	}
	return nil
}

type PendingOperationsPage struct {
	Operations []OperationDescriptor `json:"operations"`
	Cursor     *string               `json:"cursor"`
}

func (page PendingOperationsPage) Validate() error {
	if page.Operations == nil || len(page.Operations) > 64 {
		return NewFault(InvalidState)
	}
	if page.Cursor != nil && *page.Cursor == "" {
		return NewFault(InvalidState)
	}
	for _, descriptor := range page.Operations {
		if err := descriptor.ValidatePending(); err != nil {
			return err
		}
	}
	return nil
}

type PendingOperationsResponse Envelope[PendingOperationsPage]

func (response PendingOperationsResponse) Validate() error {
	return Envelope[PendingOperationsPage](response).Validate()
}

// The application sees committed descriptors only; SQL stays in its adapter.
type PendingOperationsReader interface {
	ListPendingOperations(context.Context, PendingOperationsRequest) (PendingOperationsPage, error)
}
