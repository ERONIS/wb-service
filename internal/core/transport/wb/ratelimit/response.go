package core_transport_wb_ratelimit

import (
	"fmt"
	"net/http"
	"time"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func (registry *Registry) ObserveResponse(
	sellerScope string,
	bucketID core_transport_wb_policy.BucketID,
	response *http.Response,
) error {
	if response == nil {
		return nil
	}

	if err :=
		core_transport_wb_policy.ValidateBucketID(
			bucketID,
		); err != nil {
		return fmt.Errorf(
			"validate WB rate limiter bucket ID: %w",
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

	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	now := time.Now()

	if isRateLimited {
		retryDuration :=
			time.Duration(
				*headerValues.retrySeconds,
			) * time.Second

		limiter.applyRetryLocked(
			now,
			retryDuration,
		)
	} else {
		limiter.clampRemainingLocked(
			now,
			*headerValues.remaining,
		)
	}

	return nil
}
