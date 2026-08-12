package flowcontrol

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errBackoffContextRequired = errors.New(
	"backoff context is required",
)

// Backoff управляет задержкой между повторными read-запросами.
type Backoff struct {
	baseDelay time.Duration
}

// NewBackoff создаёт backoff с начальной задержкой.
func NewBackoff(
	baseDelay time.Duration,
) (Backoff, error) {
	backoff := Backoff{
		baseDelay: baseDelay,
	}

	if err := backoff.validate(); err != nil {
		return Backoff{}, err
	}

	return backoff, nil
}

// Wait ожидает задержку перед указанным номером повтора.
func (backoff Backoff) Wait(
	ctx context.Context,
	retryNumber int,
	retryAfter time.Duration,
) error {
	if ctx == nil {
		return errBackoffContextRequired
	}

	delay, err := backoff.delay(
		retryNumber,
		retryAfter,
	)
	if err != nil {
		return err
	}

	return waitDuration(ctx, delay)
}

func (backoff Backoff) delay(
	retryNumber int,
	retryAfter time.Duration,
) (time.Duration, error) {
	if err := backoff.validate(); err != nil {
		return 0, err
	}
	if retryNumber <= 0 {
		return 0, fmt.Errorf(
			"backoff retry number must be positive: %d",
			retryNumber,
		)
	}

	delay := backoff.baseDelay

	for currentRetry := 1; currentRetry < retryNumber; currentRetry++ {
		if delay > time.Duration(1<<63-1)/2 {
			return 0, fmt.Errorf(
				"backoff delay overflows for retry number %d",
				retryNumber,
			)
		}

		delay *= 2
	}

	if retryAfter > delay {
		delay = retryAfter
	}

	return delay, nil
}

func (backoff Backoff) validate() error {
	if backoff.baseDelay <= 0 {
		return fmt.Errorf(
			"backoff base delay must be positive: %s",
			backoff.baseDelay,
		)
	}

	return nil
}
