package response

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
)

// IsRetryableTransportError проверяет ограниченный allowlist transport errors.
func IsRetryableTransportError(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var networkErr net.Error

	return errors.As(err, &networkErr) &&
		networkErr.Timeout()
}
