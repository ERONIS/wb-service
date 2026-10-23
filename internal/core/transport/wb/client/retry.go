package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// executeAttempts проводит отправки с ожиданием лимита и разрешённых повторов.
func (client *APIClient) executeAttempts(
	ctx context.Context,
	prepared request.Prepared,
	operation policy.Operation,
	trace *requestTrace,
	maxAttempts int,
) executionResult {
	result := executionResult{delivery: NotDispatched}
	executionDeadline, _ := ctx.Deadline()

	for attemptNumber := 1; attemptNumber <= maxAttempts; attemptNumber++ {
		if attemptNumber > 1 {
			if err := ctx.Err(); err != nil {
				return retryInterruptedResult(result, err)
			}
		}
		waitStartedAt := time.Now()
		waitErr := client.rateLimiters.Wait(
			ctx,
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
				return retryInterruptedResult(result, waitResult.err)
			}
			return waitResult
		}
		attemptContext := ctx
		cancelAttempt := func() {}
		remainingAttempts := maxAttempts - attemptNumber + 1
		if remainingAttempts > 1 {
			attemptContext, cancelAttempt = context.WithTimeout(
				ctx,
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
		if !shouldRetry(ctx, result, attemptNumber, maxAttempts) {
			break
		}
		if err := waitRetry(ctx, attemptNumber, result.retryAfter); err != nil {
			return retryInterruptedResult(result, err)
		}
	}
	return result
}

// attemptLimit выбирает число попыток по правилам повторной отправки.
func attemptLimit(
	operation policy.Operation,
) int {
	if operation.RetryMode().AllowsRetry() {
		return policy.DefaultMaxReadAttempts
	}

	return 1
}

// shouldRetry проверяет, разрешена ли ещё одна попытка.
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

// waitRetry ждёт перед повтором и прекращает ожидание при отмене контекста.
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
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryInterruptedResult сохраняет причину прерванного повтора.
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
