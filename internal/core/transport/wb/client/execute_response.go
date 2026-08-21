package client

import (
	"context"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// ExecuteResponse performs one closed WB operation and returns its complete raw
// response DTO. Operation selection and response interpretation stay in the
// calling feature transport and service.
func ExecuteResponse[Response any](
	ctx context.Context,
	executor Executor,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	var response Response
	if executor == nil {
		return response, errAPIClientRequired
	}
	if err := executor.Execute(ctx, operation, query, body, &response); err != nil {
		return response, err
	}
	return response, nil
}
