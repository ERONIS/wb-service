package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/response"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// executionResult хранит итог одной попытки отправки запроса.
type executionResult struct {
	statusCode     int
	body           []byte
	err            error
	delivery       DeliveryState
	retryable      bool
	retryAfter     time.Duration
	observationErr error
}

// doAttempt создаёт HTTP-запрос, добавляет токен и отправляет одну попытку.
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

// transportErrorResult классифицирует сбой отправки и учитывает полученный ответ.
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

// responseResult читает ответ, обновляет лимиты и проверяет HTTP-статус.
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

// deliveryWithoutResponse определяет, успел ли запрос уйти без получения ответа.
func deliveryWithoutResponse(dispatched bool) DeliveryState {
	if dispatched {
		return UnknownDelivery
	}

	return NotDispatched
}
