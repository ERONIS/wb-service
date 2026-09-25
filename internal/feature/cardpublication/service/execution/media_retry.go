package execution

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
)

const mediaRequestAttempts = 5

// Retries belong to this workflow, not to all WB mutations. A file retry uses
// the same bytes, nmID and photo number: WB replaces that slot rather than
// appending another photo. Already accepted files are not sent again.
// https://dev.wildberries.ru/en/news/101
func retryMediaRequest[T any](ctx context.Context, wait func(context.Context, time.Duration) error,
	call func() (T, error),
) (T, error) {
	var result T
	var err error
	var dispatchedErr error
	for attempt := 0; attempt < mediaRequestAttempts; attempt++ {
		if ctx.Err() != nil {
			return result, errors.Join(dispatchedErr, err, ctx.Err())
		}
		result, err = call()
		if err == nil {
			return result, nil
		}
		var classified core_wb.ClassifiedError
		if errors.As(err, &classified) && classified.Delivery() != core_wb.NotDispatched {
			dispatchedErr = err
		}
		if ctx.Err() != nil || attempt == mediaRequestAttempts-1 {
			return result, errors.Join(dispatchedErr, err)
		}
		delay, retry := mediaRetryDelay(err, attempt)
		if !retry {
			return result, errors.Join(dispatchedErr, err)
		}
		if waitErr := wait(ctx, delay); waitErr != nil {
			// Keep the last delivery evidence when shutdown interrupts backoff.
			return result, errors.Join(dispatchedErr, err, waitErr)
		}
	}
	return result, err
}

func mediaRetryDelay(err error, attempt int) (time.Duration, bool) {
	delay := time.Second * time.Duration(1<<min(attempt, 5))
	var classified core_wb.ClassifiedError
	if errors.As(err, &classified) {
		status := classified.HTTPStatus()
		retry := status == 429 || status == 408 || status >= 500 && status <= 599
		if status < 400 {
			switch classified.Code() {
			case "transport_error", "context_deadline_exceeded", "empty_response", "invalid_response":
				retry = true
			}
		}
		if status == 429 {
			delay = max(delay, 10*time.Second)
		}
		return max(delay, classified.RetryAfter()), retry
	}
	var networkError net.Error
	return delay, errors.As(err, &networkError) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func waitMediaRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
