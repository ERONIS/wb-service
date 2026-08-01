package core_transport_wb_ratelimit

import (
	"context"
	"fmt"
	"time"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func (registry *Registry) Wait(
	ctx context.Context,
	sellerScope string,
	bucketIDs []core_transport_wb_policy.BucketID,
) error {
	if err :=
		core_transport_wb_policy.ValidateBucketIDs(
			bucketIDs,
		); err != nil {
		return fmt.Errorf(
			"validate WB rate limiter bucket IDs: %w",
			err,
		)
	}

	limiters, err := registry.limitersFor(
		sellerScope,
		bucketIDs,
	)
	if err != nil {
		return fmt.Errorf(
			"prepare WB rate limiters: %w",
			err,
		)
	}

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf(
				"wait for WB rate limiters: %w",
				err,
			)
		}

		lockLimiters(limiters)

		if err := ctx.Err(); err != nil {
			unlockLimiters(limiters)

			return fmt.Errorf(
				"wait for WB rate limiters: %w",
				err,
			)
		}

		waitDuration :=
			maxWaitDurationLocked(
				limiters,
				time.Now(),
			)

		if waitDuration == 0 {
			takeLimitersLocked(limiters)
		}

		unlockLimiters(limiters)

		if waitDuration == 0 {
			return nil
		}

		timer := time.NewTimer(waitDuration)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf(
				"wait for WB rate limiters: %w",
				ctx.Err(),
			)

		case <-timer.C:
		}
	}
}

func lockLimiters(
	limiters []*bucketLimiter,
) {
	for _, limiter := range limiters {
		limiter.mutex.Lock()
	}
}

func unlockLimiters(
	limiters []*bucketLimiter,
) {
	for index := len(limiters) - 1; index >= 0; index-- {
		limiters[index].mutex.Unlock()
	}
}

func maxWaitDurationLocked(
	limiters []*bucketLimiter,
	now time.Time,
) time.Duration {
	var maxWaitDuration time.Duration

	for _, limiter := range limiters {
		waitDuration :=
			limiter.waitDurationLocked(now)

		if waitDuration > maxWaitDuration {
			maxWaitDuration = waitDuration
		}
	}

	return maxWaitDuration
}
func takeLimitersLocked(
	limiters []*bucketLimiter,
) {
	for _, limiter := range limiters {
		limiter.takeLocked()
	}
}
