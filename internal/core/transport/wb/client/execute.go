package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/response"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
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
	attemptLimit := attemptLimit(operation)
	trace := newRequestTrace(client, operation, attemptLimit)
	result := executionResult{delivery: NotDispatched}

	defer func() {
		trace.complete(result, executeErr)
		trace.log(clientLogger(client))
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
	executionContext, cancelExecution := context.WithTimeout(ctx, client.httpClient.Timeout*time.Duration(attemptLimit))
	defer cancelExecution()
	executionDeadline, _ := executionContext.Deadline()

	if operation.ResponseMode().IsJSON() {
		if err := response.ValidateTarget(target); err != nil {
			result.err = newClassifiedError(
				ErrorCodeInvalidResultTarget,
				NotDispatched,
				0,
				0,
				err,
			)

			return result.err
		}
	}

	prepared, err := request.Prepare(
		operation,
		query,
		body,
	)
	if err != nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidRequest,
			NotDispatched,
			0,
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
			client.sellerID,
			operation.BucketID(),
		)
		trace.addLimiterWait(time.Since(waitStartedAt))
		if waitErr != nil {
			waitResult := executionResult{
				err: newClassifiedError(
					waitErrorCode(waitErr),
					NotDispatched,
					0,
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
			attemptContext, cancelAttempt = context.WithTimeout(
				executionContext,
				time.Until(executionDeadline)/time.Duration(remainingAttempts),
			)
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
		if !shouldRetry(
			executionContext,
			result,
			attemptNumber,
			attemptLimit,
		) {
			break
		}

		if err := waitRetry(
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
				result.statusCode,
				0,
				err,
			)

			return result.err
		}
	}

	return nil
}

func attemptLimit(
	operation policy.Operation,
) int {
	if operation.RetryMode().AllowsRetry() {
		return policy.DefaultMaxReadAttempts
	}

	return 1
}

func shouldRetry(
	ctx context.Context,
	result executionResult,
	attemptNumber int,
	attemptLimit int,
) bool {
	return ctx.Err() == nil &&
		attemptNumber < attemptLimit &&
		result.retryable
}

func waitRetry(ctx context.Context, retryNumber int, retryAfter time.Duration) error {
	delay := policy.DefaultRetryBaseDelay
	for currentRetry := 1; currentRetry < retryNumber; currentRetry++ {
		delay *= 2
	}
	if retryAfter > delay {
		delay = retryAfter
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *APIClient) doAttempt(
	ctx context.Context,
	prepared request.Prepared,
	operation policy.Operation,
	trace *requestTrace,
) executionResult {
	var dispatchCount atomic.Int64
	attemptContext := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(_ httptrace.WroteRequestInfo) {
			dispatchCount.Add(1)
		},
	})

	httpRequest, err := prepared.NewHTTPRequest(attemptContext)
	if err != nil {
		return executionResult{
			err: newClassifiedError(
				ErrorCodeInvalidRequest,
				NotDispatched,
				0,
				0,
				err,
			),
			delivery: NotDispatched,
		}
	}
	httpRequest.Header.Set("Authorization", client.token)

	httpResponse, transportErr := client.httpClient.Do(httpRequest)
	statusCode := 0
	if httpResponse != nil {
		statusCode = httpResponse.StatusCode
	}
	dispatches := int(dispatchCount.Load())
	trace.recordAttempt(statusCode, dispatches)
	dispatched := dispatches > 0
	if transportErr != nil {
		return client.transportErrorResult(
			httpResponse,
			operation,
			transportErr,
			dispatched,
		)
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
				0,
				newSafeTransportError(transportErr),
			),
			delivery:  delivery,
			retryable: response.IsRetryableTransportError(transportErr),
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
		result.statusCode,
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
		client.sellerID,
		operation.BucketID(),
		httpResponse,
	)

	var readErr error
	if operation.ResponseMode().IsJSON() {
		result.body, readErr = response.ReadAndClose(
			httpResponse,
			operation.MaxResponseBytes(),
		)
	} else {
		readErr = response.Close(httpResponse)
	}

	if readErr != nil {
		result.err = newClassifiedError(
			ErrorCodeInvalidResponse,
			ResponseReceived,
			result.statusCode,
			0,
			readErr,
		)
		result.retryable = response.IsRetryableTransportError(readErr)

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
	message := "WB API request failed"
	if summary := response.ErrorSummary(result.body); summary != "" {
		message += ": " + summary
	}
	result.err = newClassifiedError(
		code,
		ResponseReceived,
		result.statusCode,
		result.retryAfter,
		errors.New(message),
	)

	return result
}

func retryInterruptedResult(
	result executionResult,
	cause error,
) executionResult {
	retryCause := cause
	if result.err != nil {
		retryCause = errors.Join(result.err, cause)
	}

	result.err = newClassifiedError(
		ErrorCodeRetryInterrupted,
		result.delivery,
		result.statusCode,
		0,
		fmt.Errorf("WB read retry interrupted: %w", retryCause),
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
