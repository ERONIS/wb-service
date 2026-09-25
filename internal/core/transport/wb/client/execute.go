package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ERONIS/wb-service/internal/core/observability"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/response"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	transport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
)

type executionResult struct {
	statusCode     int
	body           []byte
	err            error
	delivery       DeliveryState
	retryable      bool
	retryAfter     time.Duration
	observationErr error
}

// Execute выполняет полный lifecycle одного логического WB-запроса.
func (client *APIClient) Execute(
	ctx context.Context,
	operation policy.Operation,
	query any,
	body any,
	target any,
) (executeErr error) {
	attemptLimit := client.attemptLimit(operation)
	trace := newRequestTrace(client, operation, attemptLimit)
	result := executionResult{delivery: NotDispatched}

	defer func() {
		var classified *classifiedError
		if errors.As(executeErr, &classified) && classified.delivery == ResponseReceived {
			classified.httpStatus = result.statusCode
		}
		trace.complete(result, executeErr)
		trace.log(observability.LoggerWithContext(ctx, clientLogger(client)))
	}()

	if client == nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
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
			errRequestContextRequired,
		)

		return result.err
	}
	executionContext, cancelExecution := context.WithTimeout(ctx, client.httpClient.Timeout*time.Duration(attemptLimit))
	defer cancelExecution()

	if operation.ResponseMode().IsJSON() {
		if err := response.ValidateTarget(target); err != nil {
			result.err = newClassifiedError(
				ErrorCodeInvalidResultTarget,
				NotDispatched,
				0,
				err,
			)

			return result.err
		}
	}

	prepared, err := request.Prepare(
		client.baseURL,
		operation,
		query,
		body,
	)
	if err != nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
			0,
			err,
		)

		return result.err
	}
	trace.requestBytes = prepared.BodyBytes()

	for attemptNumber := 1; attemptNumber <= attemptLimit; attemptNumber++ {
		if attemptNumber > 1 {
			if err := executionContext.Err(); err != nil {
				result = retryInterruptedResult(result, err)

				return result.err
			}
		}
		waitStartedAt := time.Now()
		waitErr := client.rateLimiters.Wait(
			executionContext,
			client.cabinetID,
			operation.BucketID(),
		)
		trace.addLimiterWait(time.Since(waitStartedAt))
		if waitErr != nil {
			code := ErrorCodeRateLimited
			if errors.Is(waitErr, context.Canceled) {
				code = ErrorCodeCanceled
			} else if errors.Is(waitErr, context.DeadlineExceeded) {
				code = ErrorCodeDeadlineExceeded
			}
			waitResult := executionResult{
				err: newClassifiedError(
					code,
					NotDispatched,
					0,
					waitErr,
				),
				delivery: NotDispatched,
			}
			if attemptNumber > 1 {
				result = retryInterruptedResult(result, waitResult.err)
			} else {
				result = waitResult
			}

			return result.err
		}
		attemptContext := executionContext
		cancelAttempt := func() {}
		remainingAttempts := attemptLimit - attemptNumber + 1
		if remainingAttempts > 1 {
			if deadline, exists := executionContext.Deadline(); exists {
				remaining := time.Until(deadline)
				attemptContext, cancelAttempt = context.WithTimeout(
					executionContext,
					remaining/time.Duration(remainingAttempts),
				)
			}
		}

		attemptResult := client.doAttempt(
			attemptContext,
			prepared,
			operation,
			trace,
		)
		cancelAttempt()
		trace.addResponseBytes(len(attemptResult.body))
		trace.recordObservationError(attemptResult.observationErr)

		result = attemptResult
		if !client.shouldRetry(
			executionContext,
			operation,
			result,
			attemptNumber,
			attemptLimit,
		) {
			break
		}

		if err := client.backoff.Wait(
			executionContext,
			attemptNumber,
			result.retryAfter,
		); err != nil {
			result = retryInterruptedResult(result, err)

			return result.err
		}
	}

	if result.err != nil {
		return result.err
	}

	if operation.ResponseMode().IsJSON() {
		if err := response.Decode(result.body, target); err != nil {
			code := ErrorCodeInvalidResponse
			if response.IsEmptyBody(err) {
				code = ErrorCodeEmptyResponse
			}
			result.err = newClassifiedError(
				code,
				result.delivery,
				0,
				err,
			)

			return result.err
		}
	}

	return nil
}

func (client *APIClient) attemptLimit(
	operation policy.Operation,
) int {
	if client != nil &&
		operation.Kind().IsRead() &&
		operation.RetryMode().AllowsRetry() {
		return client.maxReadAttempts
	}

	return 1
}

func (client *APIClient) shouldRetry(
	ctx context.Context,
	operation policy.Operation,
	result executionResult,
	attemptNumber int,
	attemptLimit int,
) bool {
	return ctx.Err() == nil &&
		attemptNumber < attemptLimit &&
		operation.Kind().IsRead() &&
		operation.RetryMode().AllowsRetry() &&
		result.retryable
}

func (client *APIClient) doAttempt(
	ctx context.Context,
	prepared request.Prepared,
	operation policy.Operation,
	trace *requestTrace,
) executionResult {
	recorder := &attemptRecorder{}
	attemptContext := transport.WithAttemptRecorder(ctx, recorder)

	httpRequest, err := prepared.NewHTTPRequest(attemptContext)
	if err != nil {
		return executionResult{
			err: newClassifiedError(
				ErrorCodeInvalidRequest,
				NotDispatched,
				0,
				err,
			),
			delivery: NotDispatched,
		}
	}

	httpResponse, transportErr := client.httpClient.Do(httpRequest)
	attempt := recorder.snapshot(httpResponse)
	trace.recordAttempt(attempt)

	dispatched := attempt.DispatchCount > 0
	if transportErr != nil {
		return client.transportErrorResult(
			httpResponse,
			operation,
			transportErr,
			dispatched,
		)
	}
	if httpResponse == nil {
		delivery := deliveryWithoutResponse(dispatched)

		return executionResult{
			err: newClassifiedError(
				ErrorCodeTransport,
				delivery,
				0,
				errHTTPResponseRequired,
			),
			delivery: delivery,
		}
	}

	return client.responseResult(httpResponse, operation)
}

func (client *APIClient) transportErrorResult(
	httpResponse *http.Response,
	operation policy.Operation,
	transportErr error,
	dispatched bool,
) executionResult {
	if httpResponse == nil {
		delivery := deliveryWithoutResponse(dispatched)

		return executionResult{
			err: newClassifiedError(
				ErrorCodeTransport,
				delivery,
				0,
				newSafeTransportError(transportErr),
			),
			delivery: delivery,
			// Transport failures remain retryable while the shared logical
			// request deadline still has budget for another attempt.
			retryable: response.IsRetryableTransportError(transportErr) ||
				errors.Is(transportErr, context.DeadlineExceeded),
		}
	}

	result := client.responseResult(httpResponse, operation)
	cause := transportErr
	if result.err != nil {
		cause = errors.Join(transportErr, result.err)
	}

	result.err = newClassifiedError(
		ErrorCodeTransport,
		ResponseReceived,
		result.retryAfter,
		newSafeTransportError(cause),
	)
	result.delivery = ResponseReceived
	result.retryable = false

	return result
}

func (client *APIClient) responseResult(
	httpResponse *http.Response,
	operation policy.Operation,
) executionResult {
	result := executionResult{
		statusCode: httpResponse.StatusCode,
		delivery:   ResponseReceived,
	}

	result.retryAfter, result.observationErr = client.rateLimiters.Observe(
		client.cabinetID,
		operation.BucketID(),
		httpResponse,
	)

	var readErr error
	switch {
	case operation.ResponseMode().IsJSON():
		result.body, readErr = response.ReadAndClose(
			httpResponse,
			operation.MaxResponseBytes(),
		)

	case operation.ResponseMode().IsNone():
		readErr = response.Close(httpResponse)

	default:
		readErr = fmt.Errorf(
			"WB response has unsupported body mode %q",
			operation.ResponseMode(),
		)
		if httpResponse.Body != nil {
			readErr = errors.Join(readErr, httpResponse.Body.Close())
		}
	}

	if readErr != nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidResponse,
			ResponseReceived,
			0,
			readErr,
		)
		// A safe read may be repeated when WB returned headers but the body
		// was interrupted. shouldRetry additionally guarantees that the
		// shared logical request deadline still has time left.
		result.retryable = response.IsRetryableTransportError(readErr) ||
			errors.Is(readErr, context.DeadlineExceeded)

		return result
	}

	if operation.IsSuccessStatus(result.statusCode) {
		return result
	}

	code := ErrorCodeUnexpectedStatus
	if result.statusCode == http.StatusTooManyRequests {
		code = ErrorCodeRateLimited
	}
	result.retryable = response.IsRetryableStatus(result.statusCode)
	result.err = newClassifiedError(
		code,
		ResponseReceived,
		result.retryAfter,
		fmt.Errorf(
			"WB API returned HTTP status %d",
			result.statusCode,
		),
	)

	return result
}

func retryInterruptedResult(
	result executionResult,
	cause error,
) executionResult {
	if cause == nil {
		return result
	}

	retryCause := cause
	if result.err != nil {
		retryCause = errors.Join(result.err, cause)
	}

	result.err = newClassifiedError(
		ErrorCodeRetryInterrupted,
		result.delivery,
		0,
		newSafeWrappedError(
			"WB read retry interrupted",
			retryCause,
		),
	)
	result.retryable = false

	return result
}

func deliveryWithoutResponse(dispatched bool) DeliveryState {
	if dispatched {
		return UnknownDelivery
	}

	return NotDispatched
}
