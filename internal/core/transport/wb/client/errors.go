package client

import (
	"errors"
	"time"
)

var (
	errAPICabinetIDRequired = errors.New(
		"WB API cabinet ID is required",
	)
	errAPICabinetNameRequired = errors.New(
		"WB API cabinet name is required",
	)
	errAPIBaseURLRequired = errors.New(
		"WB API base URL is required",
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
	errAPIMaxReadAttemptsRequired = errors.New(
		"WB API max read attempts must be positive",
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
	errHTTPResponseRequired = errors.New(
		"WB HTTP response is required",
	)
)

const (
	ErrorCodeInvalidRequest      = "invalid_request"
	ErrorCodeInvalidResultTarget = "invalid_result_target"
	ErrorCodeInvalidResponse     = "invalid_response"
	ErrorCodeRateLimited         = "rate_limited"
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
	RetryAfter() time.Duration
}

type classifiedError struct {
	code       string
	delivery   DeliveryState
	retryAfter time.Duration
	cause      error
}

type safeWrappedError struct {
	message string
	cause   error
}

func newClassifiedError(
	code string,
	delivery DeliveryState,
	retryAfter time.Duration,
	cause error,
) ClassifiedError {
	return &classifiedError{
		code:       code,
		delivery:   delivery,
		retryAfter: retryAfter,
		cause:      cause,
	}
}

func newSafeWrappedError(message string, cause error) error {
	return &safeWrappedError{
		message: message,
		cause:   cause,
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
	if classifiedErr.cause == nil {
		return classifiedErr.code
	}

	return classifiedErr.cause.Error()
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

func (classifiedErr *classifiedError) RetryAfter() time.Duration {
	return classifiedErr.retryAfter
}

func (wrappedErr *safeWrappedError) Error() string {
	return wrappedErr.message
}

func (wrappedErr *safeWrappedError) Unwrap() error {
	return wrappedErr.cause
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
