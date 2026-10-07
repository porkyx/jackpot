package desktop

import "github.com/porkyx/jackpot/internal/contracts"

// checkedQuery preserves a complete, validated response or returns a typed
// query fault. Durable mutation outcomes do not pass through this read limit.
func checkedQuery[T any](response contracts.Envelope[T]) (contracts.Envelope[T], error) {
	if err := response.Validate(); err != nil {
		return response, err
	}
	if err := contracts.ValidateQueryJSONSize(response); err != nil {
		failure := failureEnvelope[T](response.ResponseHeader, err)
		failure.OperationID = response.OperationID
		failure.Revision = response.Revision
		return failure, failure.Validate()
	}
	return response, nil
}
