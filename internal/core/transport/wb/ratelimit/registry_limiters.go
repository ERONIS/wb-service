package core_transport_wb_ratelimit

import (
	"fmt"
	"slices"
	"strings"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func (registry *Registry) limiterFor(
	sellerScope string,
	bucketID core_transport_wb_policy.BucketID,
) (*bucketLimiter, error) {
	sellerScope = strings.TrimSpace(sellerScope)
	if sellerScope == "" {
		return nil, fmt.Errorf("get WB rate limiter: seller scope is empty")
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	key := limiterKey{
		sellerScope: sellerScope,
		bucketID:    bucketID,
	}
	limiter, limiterExist := registry.limiters[key]
	if limiterExist {
		return limiter, nil
	}

	bucketPolicy, bucketPolicyExist := registry.policies[bucketID]

	if !bucketPolicyExist {
		return nil, fmt.Errorf(
			"get WB rate limiter: bucket policy %q is not registered",
			bucketID,
		)
	}
	limiter = newBucketLimiter(bucketPolicy)
	registry.limiters[key] = limiter

	return limiter, nil
}
func (registry *Registry) limitersFor(
	sellerScope string,
	bucketIDs []core_transport_wb_policy.BucketID,
) ([]*bucketLimiter, error) {
	limiters := make(
		[]*bucketLimiter,
		0,
		len(bucketIDs),
	)

	for _, bucketID := range bucketIDs {
		limiter, err := registry.limiterFor(
			sellerScope,
			bucketID,
		)
		if err != nil {
			return nil, err
		}

		limiters = append(
			limiters,
			limiter,
		)
	}

	slices.SortFunc(
		limiters,
		func(
			left *bucketLimiter,
			right *bucketLimiter,
		) int {
			return strings.Compare(
				string(left.policy.ID),
				string(right.policy.ID),
			)
		},
	)

	return limiters, nil
}
