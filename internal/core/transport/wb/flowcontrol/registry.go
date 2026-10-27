package flowcontrol

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

// Registry хранит каталог правил и лимитеры для каждой пары кабинета и корзины.
type Registry struct {
	mu       sync.Mutex
	catalog  policy.BucketCatalog
	limiters map[limiterKey]*bucketLimiter
}

type limiterKey struct {
	sellerID domain.SellerID
	bucketID policy.BucketID
}

// NewRegistry создаёт реестр лимитеров из проверенного каталога.
func NewRegistry(catalog policy.BucketCatalog) *Registry {
	return &Registry{
		catalog:  catalog,
		limiters: make(map[limiterKey]*bucketLimiter),
	}
}

// Wait ожидает разрешение лимитера для кабинета и корзины.
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

// Observe учитывает заголовки ограничения запросов из ответа WB.
func (registry *Registry) Observe(
	sellerID domain.SellerID,
	bucketID policy.BucketID,
	response *http.Response,
) (time.Duration, error) {
	limiter, err := registry.limiter(sellerID, bucketID)
	if err != nil {
		return 0, err
	}

	limits, parseErr := parseResponseRateLimits(response)
	if parseErr != nil {
		if response != nil &&
			response.StatusCode == http.StatusTooManyRequests {
			limiter.observe(responseRateLimits{
				statusCode: http.StatusTooManyRequests,
			})
		}

		return 0, parseErr
	}

	retryAfter := time.Duration(0)
	if limits.retryAfter != nil {
		retryAfter = *limits.retryAfter
	}

	limiter.observe(limits)
	return retryAfter, nil
}

// limiter возвращает существующий лимитер или создаёт новый для пары.
func (registry *Registry) limiter(
	sellerID domain.SellerID,
	bucketID policy.BucketID,
) (*bucketLimiter, error) {
	if registry == nil || registry.limiters == nil {
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

	spec, exists := registry.catalog.Spec(bucketID)
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
