package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	flowcontrol "github.com/ERONIS/wb-service/internal/core/transport/wb/flowcontrol"
)

var (
	errAPISellerIDRequired = errors.New(
		"WB API seller ID is required",
	)
	errAPICabinetNameRequired = errors.New(
		"WB API cabinet name is required",
	)
	errAPIHTTPClientRequired = errors.New(
		"WB API HTTP client is required",
	)
	errAPIRoundTripperRequired = errors.New(
		"WB API HTTP client transport is required",
	)
	errAPIHTTPClientTimeoutRequired = errors.New(
		"WB API HTTP client timeout must be positive",
	)
	errAPIRateLimitersRequired = errors.New(
		"WB API rate limiter registry is required",
	)
	errAPILoggerRequired = errors.New(
		"WB API logger is required",
	)
	errAPIClientRequired = errors.New(
		"WB API client is required",
	)
	errRequestContextRequired = errors.New(
		"WB request context is required",
	)
)

const (
	ErrorCodeInvalidRequest      = "invalid_request"
	ErrorCodeInvalidResultTarget = "invalid_result_target"
	ErrorCodeEmptyResponse       = "empty_response"
	ErrorCodeInvalidResponse     = "invalid_response"
	ErrorCodeRateLimited         = "rate_limited"
	ErrorCodeLimiterQueueFull    = "limiter_queue_full"
	ErrorCodeExecutorUnavailable = "executor_unavailable"
	ErrorCodeCanceled            = "context_canceled"
	ErrorCodeDeadlineExceeded    = "context_deadline_exceeded"
	ErrorCodeRetryInterrupted    = "retry_interrupted"
	ErrorCodeUnexpectedStatus    = "unexpected_status"
	ErrorCodeTransport           = "transport_error"
)

// DeliveryState показывает, насколько далеко дошла отправка HTTP-запроса.
type DeliveryState uint8

const (
	// NotDispatched означает, что HTTP-отправка ещё не начиналась.
	NotDispatched DeliveryState = iota

	// ResponseReceived означает, что WB вернул HTTP-ответ.
	ResponseReceived

	// UnknownDelivery означает, что отправка началась, но ответ не получен.
	UnknownDelivery
)

// ClassifiedError — ошибка WB Core с данными для программной обработки.
type ClassifiedError interface {
	error

	Code() string
	Delivery() DeliveryState
	HTTPStatus() int
	RetryAfter() time.Duration
}

type classifiedError struct {
	code       string
	delivery   DeliveryState
	httpStatus int
	retryAfter time.Duration
	cause      error
}

type safeTransportError struct {
	message string
	cause   error
}

func newClassifiedError(
	code string,
	delivery DeliveryState,
	httpStatus int,
	retryAfter time.Duration,
	cause error,
) ClassifiedError {
	return &classifiedError{
		code:       code,
		delivery:   delivery,
		httpStatus: httpStatus,
		retryAfter: retryAfter,
		cause:      cause,
	}
}

// ExecutorUnavailableError классифицирует отсутствие исполнителя до HTTP-отправки.
func ExecutorUnavailableError(cause error) ClassifiedError {
	return newClassifiedError(ErrorCodeExecutorUnavailable, NotDispatched, 0, 0, cause)
}

func newSafeTransportError(cause error) error {
	message := "WB HTTP transport failed"
	if errors.Is(cause, context.Canceled) {
		message = "WB HTTP transport canceled"
	} else if errors.Is(cause, context.DeadlineExceeded) {
		message = "WB HTTP transport timed out"
	} else {
		var networkErr net.Error
		if errors.As(cause, &networkErr) && networkErr.Timeout() {
			message = "WB HTTP transport timed out"
		}
	}

	return &safeTransportError{message: message, cause: cause}
}

func waitErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return ErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorCodeDeadlineExceeded
	case errors.Is(err, flowcontrol.ErrLimiterQueueFull):
		return ErrorCodeLimiterQueueFull
	default:
		return ErrorCodeInvalidRequest
	}
}

func (state DeliveryState) String() string {
	switch state {
	case NotDispatched:
		return "not_dispatched"

	case ResponseReceived:
		return "response_received"

	case UnknownDelivery:
		return "unknown_delivery"

	default:
		return "unknown"
	}
}

func (classifiedErr *classifiedError) Error() string {
	prefix := "WB " + classifiedErr.code
	if classifiedErr.httpStatus != 0 {
		prefix = fmt.Sprintf("%s (HTTP %d)", prefix, classifiedErr.httpStatus)
	}
	if classifiedErr.cause == nil {
		return prefix
	}

	return prefix + ": " + classifiedErr.cause.Error()
}

func (transportErr *safeTransportError) Error() string {
	return transportErr.message
}

func (transportErr *safeTransportError) Unwrap() error {
	return transportErr.cause
}

func (classifiedErr *classifiedError) Unwrap() error {
	return classifiedErr.cause
}

func (classifiedErr *classifiedError) Code() string {
	return classifiedErr.code
}

func (classifiedErr *classifiedError) Delivery() DeliveryState {
	return classifiedErr.delivery
}

func (classifiedErr *classifiedError) HTTPStatus() int {
	return classifiedErr.httpStatus
}

func (classifiedErr *classifiedError) RetryAfter() time.Duration {
	return classifiedErr.retryAfter
}

func classifiedErrorCode(err error) string {
	if err == nil {
		return ""
	}

	var classifiedErr ClassifiedError
	if errors.As(err, &classifiedErr) {
		return classifiedErr.Code()
	}

	return "unclassified"
}

var _ ClassifiedError = (*classifiedError)(nil)
