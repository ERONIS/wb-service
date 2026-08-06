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
	bucketID core_transport_wb_policy.BucketID,
) error {
	if err :=
		core_transport_wb_policy.ValidateBucketID(
			bucketID,
		); err != nil {
		return fmt.Errorf(
			"validate WB rate limiter bucket ID: %w",
			err,
		)
	}

	limiter, err := registry.limiterFor(
		sellerScope,
		bucketID,
	)
	if err != nil {
		return fmt.Errorf(
			"prepare WB rate limiter: %w",
			err,
		)
	}

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf(
				"wait for WB rate limiter: %w",
				err,
			)
		}

		limiter.mutex.Lock()

		if err := ctx.Err(); err != nil {
			limiter.mutex.Unlock()

			return fmt.Errorf(
				"wait for WB rate limiter: %w",
				err,
			)
		}

		waitDuration :=
			limiter.waitDurationLocked(
				time.Now(),
			)

		if waitDuration == 0 {
			limiter.takeLocked()
		}

		limiter.mutex.Unlock()

		if waitDuration == 0 {
			return nil
		}

		timer := time.NewTimer(waitDuration)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf(
				"wait for WB rate limiter: %w",
				ctx.Err(),
			)

		case <-timer.C:
		}
	}
}
