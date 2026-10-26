package response

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
)

// IsRetryableTransportError распознаёт обрыв ответа и сетевой тайм-аут.
func IsRetryableTransportError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}

	var networkErr net.Error
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &networkErr) && networkErr.Timeout())
}

// IsRetryableStatus определяет, допускает ли ответ повторную попытку.
func IsRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true

	default:
		return false
	}
}
