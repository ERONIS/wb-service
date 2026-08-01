package core_transport_wb_ratelimit

import (
	"fmt"
	"net/http"
	"time"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func (registry *Registry) ObserveResponse(
	sellerScope string,
	bucketIDs []core_transport_wb_policy.BucketID,
	response *http.Response,
) error {
	if response == nil {
		return nil
	}

	if err :=
		core_transport_wb_policy.ValidateBucketIDs(
			bucketIDs,
		); err != nil {
		return fmt.Errorf(
			"validate WB rate limiter bucket IDs: %w",
			err,
		)
	}

	headerValues, err :=
		parseRateLimitHeaders(response.Header)
	if err != nil {
		return fmt.Errorf(
			"parse WB rate limit response headers: %w",
			err,
		)
	}

	isRateLimited :=
		response.StatusCode ==
			http.StatusTooManyRequests

	if isRateLimited {
		if headerValues.retrySeconds == nil {
			return nil
		}
	} else {
		if headerValues.remaining == nil {
			return nil
		}
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

	lockLimiters(limiters)

	now := time.Now()

	if isRateLimited {
		retryDuration :=
			time.Duration(
				*headerValues.retrySeconds,
			) * time.Second

		for _, limiter := range limiters {
			limiter.applyRetryLocked(
				now,
				retryDuration,
			)
		}
	} else {
		for _, limiter := range limiters {
			limiter.clampRemainingLocked(
				now,
				*headerValues.remaining,
			)
		}
	}

	unlockLimiters(limiters)

	return nil
}
