package flowcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

var (
	errRegistryRequired = errors.New(
		"rate limiter registry is required",
	)
	errRegistrySpecsRequired = errors.New(
		"rate limiter registry bucket specs are required",
	)
	errRegistryContextRequired = errors.New(
		"rate limiter registry context is required",
	)
	errRegistryCabinetIDRequired = errors.New(
		"rate limiter registry cabinet ID is required",
	)
	errRegistryBucketIDRequired = errors.New(
		"rate limiter registry bucket ID is required",
	)
)

// ErrBucketNotRegistered означает отсутствие BucketSpec в registry.
var ErrBucketNotRegistered = errors.New(
	"rate limiter bucket is not registered",
)

type limiterKey struct {
	cabinetID config.CabinetID
	bucketID  policy.BucketID
}

// Registry хранит политики и limiter для каждой пары cabinet и bucket.
type Registry struct {
	mutex    sync.Mutex
	policies map[policy.BucketID]policy.BucketSpec
	limiters map[limiterKey]*Limiter
}

// NewRegistry создаёт registry из закрытого каталога bucket-политик.
func NewRegistry(
	specs []policy.BucketSpec,
) (*Registry, error) {
	if len(specs) == 0 {
		return nil, errRegistrySpecsRequired
	}

	policies := make(
		map[policy.BucketID]policy.BucketSpec,
		len(specs),
	)

	for index, spec := range specs {
		if err := spec.Validate(); err != nil {
			return nil, fmt.Errorf(
				"invalid bucket spec at index %d: %w",
				index,
				err,
			)
		}
		if _, exists := policies[spec.ID]; exists {
			return nil, fmt.Errorf(
				"duplicate rate limiter bucket ID %q",
				spec.ID,
			)
		}

		policies[spec.ID] = spec
	}

	return &Registry{
		policies: policies,
		limiters: make(map[limiterKey]*Limiter),
	}, nil
}

// Wait ожидает admission для одной пары cabinet и bucket.
func (registry *Registry) Wait(
	ctx context.Context,
	cabinetID config.CabinetID,
	bucketID policy.BucketID,
) error {
	if ctx == nil {
		return errRegistryContextRequired
	}

	limiter, err := registry.limiter(
		cabinetID,
		bucketID,
	)
	if err != nil {
		return err
	}

	return limiter.Wait(ctx)
}

// Observe применяет разрешённые rate-limit headers ответа WB.
func (registry *Registry) Observe(
	cabinetID config.CabinetID,
	bucketID policy.BucketID,
	response *http.Response,
) (time.Duration, error) {
	limiter, err := registry.limiter(
		cabinetID,
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
	cabinetID config.CabinetID,
	bucketID policy.BucketID,
) (*Limiter, error) {
	if registry == nil ||
		registry.policies == nil ||
		registry.limiters == nil {
		return nil, errRegistryRequired
	}
	if cabinetID == "" {
		return nil, errRegistryCabinetIDRequired
	}
	if bucketID == "" {
		return nil, errRegistryBucketIDRequired
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	spec, exists := registry.policies[bucketID]
	if !exists {
		return nil, fmt.Errorf(
			"%w: %q",
			ErrBucketNotRegistered,
			bucketID,
		)
	}

	key := limiterKey{
		cabinetID: cabinetID,
		bucketID:  bucketID,
	}
	if limiter, exists := registry.limiters[key]; exists {
		return limiter, nil
	}

	limiter, err := NewLimiter(spec)
	if err != nil {
		return nil, err
	}

	registry.limiters[key] = limiter

	return limiter, nil
}
