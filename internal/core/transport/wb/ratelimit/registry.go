package core_transport_wb_ratelimit

import (
	"fmt"
	"sync"

	core_transport_wb_policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type Registry struct {
	mutex    sync.RWMutex
	policies map[core_transport_wb_policy.BucketID]core_transport_wb_policy.BucketPolicy
	limiters map[limiterKey]*bucketLimiter
}
type limiterKey struct {
	sellerScope string
	bucketID    core_transport_wb_policy.BucketID
}

func NewRegistry() *Registry {
	return &Registry{
		policies: make(
			map[core_transport_wb_policy.BucketID]core_transport_wb_policy.BucketPolicy,
		),
		limiters: make(
			map[limiterKey]*bucketLimiter,
		),
	}
}

func (registry *Registry) Register(
	bucketPolicy core_transport_wb_policy.BucketPolicy,
) error {
	if err := bucketPolicy.Validate(); err != nil {
		return fmt.Errorf(
			"validate WB bucket policy: %w",
			err,
		)
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	registeredPolicy, exists :=
		registry.policies[bucketPolicy.ID]

	if !exists {
		registry.policies[bucketPolicy.ID] = bucketPolicy
		return nil
	}

	if registeredPolicy == bucketPolicy {
		return nil
	}

	return fmt.Errorf(
		"WB bucket policy %q conflicts with registered policy: "+
			"registered interval=%s burst=%d, "+
			"new interval=%s burst=%d",
		bucketPolicy.ID,
		registeredPolicy.Interval,
		registeredPolicy.Burst,
		bucketPolicy.Interval,
		bucketPolicy.Burst,
	)

}
