package client

import (
	"context"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func ExecuteResponse[Response any](
	ctx context.Context,
	executor Executor,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	var response Response
	if executor == nil {
		return response, ExecutorUnavailableError(errAPIClientRequired)
	}
	if err := executor.Execute(ctx, operation, query, body, &response); err != nil {
		return response, err
	}
	return response, nil
}
