package flowcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type limiterKey struct {
	sellerID domain.SellerID
	bucketID policy.BucketID
}

// Registry хранит политики и limiter для каждой пары cabinet и bucket.
type Registry struct {
	mu       sync.Mutex
	specs    map[policy.BucketID]policy.BucketSpec
	limiters map[limiterKey]*bucketLimiter
}

// NewRegistry создаёт registry из закрытого каталога bucket-политик.
func NewRegistry(
	specs []policy.BucketSpec,
) (*Registry, error) {
	if err := policy.ValidateBucketSpecs(specs); err != nil {
		return nil, err
	}

	registeredSpecs := make(
		map[policy.BucketID]policy.BucketSpec,
		len(specs),
	)

	for _, spec := range specs {
		registeredSpecs[spec.ID] = spec
	}

	return &Registry{
		specs:    registeredSpecs,
		limiters: make(map[limiterKey]*bucketLimiter),
	}, nil
}

// Wait ожидает admission для одной пары cabinet и bucket.
func (registry *Registry) Wait(
	ctx context.Context,
	sellerID domain.SellerID,
	bucketID policy.BucketID,
) error {
	if ctx == nil {
		return errRegistryContextRequired
	}

	limiter, err := registry.limiter(
		sellerID,
		bucketID,
	)
	if err != nil {
		return err
	}

	return limiter.wait(ctx)
}

// Observe применяет разрешённые rate-limit headers ответа WB.
func (registry *Registry) Observe(
	sellerID domain.SellerID,
	bucketID policy.BucketID,
	response *http.Response,
) (time.Duration, error) {
	limiter, err := registry.limiter(
		sellerID,
		bucketID,
	)
	if err != nil {
		return 0, err
	}

	limits, parseErr := parseResponseRateLimits(response)
	if parseErr != nil {
		if response != nil &&
			response.StatusCode == http.StatusTooManyRequests {
			fallbackErr := limiter.observe(
				responseRateLimits{
					statusCode: http.StatusTooManyRequests,
				},
			)

			return 0, errors.Join(parseErr, fallbackErr)
		}

		return 0, parseErr
	}

	retryAfter := time.Duration(0)
	if limits.retryAfter != nil {
		retryAfter = *limits.retryAfter
	}

	return retryAfter, limiter.observe(limits)
}

func (registry *Registry) limiter(
	sellerID domain.SellerID,
	bucketID policy.BucketID,
) (*bucketLimiter, error) {
	if registry == nil ||
		registry.specs == nil ||
		registry.limiters == nil {
		return nil, errRegistryRequired
	}
	if sellerID == "" {
		return nil, errRegistrySellerIDRequired
	}
	if bucketID == "" {
		return nil, errRegistryBucketIDRequired
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	spec, exists := registry.specs[bucketID]
	if !exists {
		return nil, fmt.Errorf(
			"%w: %q",
			ErrBucketNotRegistered,
			bucketID,
		)
	}

	key := limiterKey{
		sellerID: sellerID,
		bucketID: bucketID,
	}
	if limiter, exists := registry.limiters[key]; exists {
		return limiter, nil
	}

	limiter := newBucketLimiter(spec)
	registry.limiters[key] = limiter

	return limiter, nil
}
