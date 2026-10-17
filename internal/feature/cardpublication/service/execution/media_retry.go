package execution

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

const mediaDownloadAttempts = 5

func retryMediaDownload(ctx context.Context, wait func(context.Context, time.Duration) error,
	call func() (contentapi.DownloadedMediaFile, error),
) (contentapi.DownloadedMediaFile, error) {
	var result contentapi.DownloadedMediaFile
	var err error
	for attempt := 0; attempt < mediaDownloadAttempts; attempt++ {
		if ctx.Err() != nil {
			return result, errors.Join(err, ctx.Err())
		}
		result, err = call()
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil || attempt == mediaDownloadAttempts-1 {
			return result, err
		}
		delay, retry := mediaDownloadRetryDelay(err, attempt)
		if !retry {
			return result, err
		}
		if waitErr := wait(ctx, delay); waitErr != nil {
			return result, errors.Join(err, waitErr)
		}
	}
	return result, err
}

func mediaDownloadRetryDelay(err error, attempt int) (time.Duration, bool) {
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
