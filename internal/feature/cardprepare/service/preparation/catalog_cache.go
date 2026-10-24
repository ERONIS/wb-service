package preparation

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

const maxCatalogCacheBytes = 128 << 20
const schemaCacheTTL = 10 * time.Minute

type catalogCacheKey struct {
	cabinet   core_domain.SellerID
	operation string
	query     string
}

type sharedCatalog struct {
	CatalogTransport
	*catalogCache
}

type catalogCache struct {
	mu       sync.Mutex
	entries  map[catalogCacheKey]catalogCacheEntry
	inflight map[catalogCacheKey]*catalogCacheCall
	bytes    int
	now      func() time.Time
}

type catalogCacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

type catalogCacheCall struct {
	done chan struct{}
}

func newCatalogCache() *catalogCache {
	return &catalogCache{
		entries:  make(map[catalogCacheKey]catalogCacheEntry),
		inflight: make(map[catalogCacheKey]*catalogCacheCall),
		now:      time.Now,
	}
}

func (cache *catalogCache) evictExpired(now time.Time) {
	for key, entry := range cache.entries {
		if now.Before(entry.expiresAt) {
			continue
		}
		cache.bytes -= len(entry.payload)
		delete(cache.entries, key)
	}
}

func cachedCatalogRead[T any](
	ctx context.Context,
	cache *catalogCache,
	cabinet core_domain.SellerID,
	operation string,
	query any,
	read func() (T, error),
	cacheable func(T) bool,
) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	encodedQuery, err := json.Marshal(query)
	if err != nil {
		return read()
	}
	key := catalogCacheKey{cabinet: cabinet, operation: operation, query: string(encodedQuery)}
	for {
		cache.mu.Lock()
		if entry, ok := cache.entries[key]; ok {
			if cache.now().Before(entry.expiresAt) {
				payload := append([]byte(nil), entry.payload...)
				cache.mu.Unlock()
				var value T
				if json.Unmarshal(payload, &value) == nil {
					return value, nil
				}
				cache.mu.Lock()
				if current, exists := cache.entries[key]; exists && current.expiresAt == entry.expiresAt {
					cache.bytes -= len(current.payload)
					delete(cache.entries, key)
				}
				cache.mu.Unlock()
				continue
			}
			cache.bytes -= len(entry.payload)
			delete(cache.entries, key)
		}
		if call, ok := cache.inflight[key]; ok {
			done := call.done
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-done:
				continue
			}
		}
		call := &catalogCacheCall{done: make(chan struct{})}
		cache.inflight[key] = call
		cache.mu.Unlock()

		value, err := read()
		var payload []byte
		if err == nil && cacheable(value) {
			payload, _ = json.Marshal(value)
		}
		cache.mu.Lock()
		if cache.bytes+len(payload) > maxCatalogCacheBytes {
			cache.evictExpired(cache.now())
		}
		if len(payload) > 0 && cache.bytes+len(payload) <= maxCatalogCacheBytes {
			cache.entries[key] = catalogCacheEntry{
				payload:   payload,
				expiresAt: cache.now().Add(schemaCacheTTL),
			}
			cache.bytes += len(payload)
		}
		delete(cache.inflight, key)
		close(call.done)
		cache.mu.Unlock()
		return value, err
	}
}

func (cache *sharedCatalog) Subjects(ctx context.Context, cabinet core_domain.SellerID, query contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	return cachedCatalogRead(ctx, cache.catalogCache, cabinet, "Subjects", query,
		func() (contentapi.SubjectsResponse, error) {
			return cache.CatalogTransport.Subjects(ctx, cabinet, query)
		},
		func(value contentapi.SubjectsResponse) bool { return !value.Error },
	)
}

func (cache *sharedCatalog) SubjectCharacteristics(ctx context.Context, cabinet core_domain.SellerID, subjectID int64, query contentapi.SubjectCharacteristicsQuery) (contentapi.SubjectCharacteristicsResponse, error) {
	return cachedCatalogRead(ctx, cache.catalogCache, cabinet, "SubjectCharacteristics", struct {
		SubjectID int64
		Query     contentapi.SubjectCharacteristicsQuery
	}{subjectID, query},
		func() (contentapi.SubjectCharacteristicsResponse, error) {
			return cache.CatalogTransport.SubjectCharacteristics(ctx, cabinet, subjectID, query)
		},
		func(value contentapi.SubjectCharacteristicsResponse) bool { return !value.Error },
	)
}
