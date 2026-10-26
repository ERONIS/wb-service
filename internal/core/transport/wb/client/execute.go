package client

import (
	"context"
	"time"

	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/response"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// ExecuteResponse выполняет операцию и возвращает ответ нужного типа.
func ExecuteResponse[Response any](
	ctx context.Context,
	executor *APIClient,
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

// Execute выполняет полный цикл одного логического запроса к WB.
func (client *APIClient) Execute(
	ctx context.Context,
	operation policy.Operation,
	query any,
	body any,
	target any,
) (executeErr error) {
	maxAttempts := attemptLimit(operation)
	trace := newRequestTrace(client, operation, maxAttempts)
	result := executionResult{delivery: NotDispatched}

	defer func() {
		trace.log(clientLogger(client), result, executeErr)
	}()

	if client == nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
			0,
			0,
			errAPIClientRequired,
		)
		return result.err
	}
	if ctx == nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
			0,
			0,
			errRequestContextRequired,
		)
		return result.err
	}

	executionContext, cancelExecution := context.WithTimeout(
		ctx,
		client.httpClient.Timeout*time.Duration(maxAttempts),
	)
	defer cancelExecution()

	prepared, err := prepareExecutionRequest(operation, query, body, target)
	if err != nil {
		result.err = err
		return result.err
	}
	trace.metrics.requestBytes = prepared.BodyBytes()

	result = client.executeAttempts(executionContext, prepared, operation, trace, maxAttempts)
	if result.err != nil {
		return result.err
	}

	result.err = decodeExecutionResponse(result, operation, target)
	return result.err
}

// prepareExecutionRequest проверяет приёмник ответа и готовит данные запроса.
func prepareExecutionRequest(
	operation policy.Operation,
	query any,
	body any,
	target any,
) (request.Prepared, error) {
	if operation.ResponseMode().IsJSON() {
		if err := response.ValidateTarget(target); err != nil {
			return request.Prepared{}, newClassifiedError(
				ErrorCodeInvalidResultTarget,
				NotDispatched,
				0,
				0,
				err,
			)
		}
	}

	prepared, err := request.Prepare(operation, query, body)
	if err != nil {
		return request.Prepared{}, newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
			0,
			0,
			err,
		)
	}
	return prepared, nil
}

// decodeExecutionResponse переносит JSON-ответ в результат и классифицирует ошибку.
func decodeExecutionResponse(
	result executionResult,
	operation policy.Operation,
	target any,
) error {
	if !operation.ResponseMode().IsJSON() {
		return nil
	}
	if err := response.Decode(result.body, target); err != nil {
		code := ErrorCodeInvalidResponse
		if response.IsEmptyBody(err) {
			code = ErrorCodeEmptyResponse
		}
		return newClassifiedError(
			code,
			result.delivery,
			result.statusCode,
			0,
			err,
		)
	}
	return nil
}
