package preparation

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

// ForBatch shares successful catalog reads within one preparation pass. Stable
// references live for the pass; capacity is refreshed periodically so hundreds
// of groups cannot enqueue identical limit requests.
func (preparer *Preparer) ForBatch() *Preparer {
	return &Preparer{
		transport: &batchCatalog{
			CatalogTransport: preparer.transport,
			entries:          make(map[catalogCacheKey]catalogCacheEntry),
			inflight:         make(map[catalogCacheKey]*catalogCacheCall),
			now:              time.Now,
		},
		now:    preparer.now,
		logger: preparer.logger,
	}
}

type catalogCacheKey struct {
	cabinet   CabinetID
	operation string
	query     string
}

type batchCatalog struct {
	CatalogTransport
	mu              sync.Mutex
	entries         map[catalogCacheKey]catalogCacheEntry
	inflight        map[catalogCacheKey]*catalogCacheCall
	bytes           int
	hits            uint64
	misses          uint64
	coalescedWaits  uint64
	stores          uint64
	skippedCapacity uint64
	evictions       uint64
	now             func() time.Time
}

const maxCatalogCacheBytes = 128 << 20
const limitsCacheTTL = 10 * time.Minute

type catalogCacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

type catalogCacheCall struct {
	done chan struct{}
}

type catalogCacheStats struct {
	Entries         int
	Bytes           int
	Hits            uint64
	Misses          uint64
	CoalescedWaits  uint64
	Stores          uint64
	SkippedCapacity uint64
	Evictions       uint64
}

func (cache *batchCatalog) stats() catalogCacheStats {
	if cache == nil {
		return catalogCacheStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return catalogCacheStats{
		Entries:         len(cache.entries),
		Bytes:           cache.bytes,
		Hits:            cache.hits,
		Misses:          cache.misses,
		CoalescedWaits:  cache.coalescedWaits,
		Stores:          cache.stores,
		SkippedCapacity: cache.skippedCapacity,
		Evictions:       cache.evictions,
	}
}

func cachedCatalogRead[T any](ctx context.Context, cache *batchCatalog, cabinet CabinetID, operation string, query any, read func() (T, error), cacheable func(T) bool) (T, error) {
	return cachedCatalogReadWithTTL(ctx, cache, cabinet, operation, query, 0, read, cacheable)
}

func cachedCatalogReadWithTTL[T any](ctx context.Context, cache *batchCatalog, cabinet CabinetID, operation string, query any, ttl time.Duration, read func() (T, error), cacheable func(T) bool) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	encodedQuery, err := json.Marshal(query)
	if err != nil {
		return read()
	}
	key := catalogCacheKey{cabinet: cabinet, operation: operation, query: string(encodedQuery)}
	missRecorded := false
	for {
		cache.mu.Lock()
		now := cache.now()
		if entry, ok := cache.entries[key]; ok {
			if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
				payload := append([]byte(nil), entry.payload...)
				cache.mu.Unlock()
				var value T
				if json.Unmarshal(payload, &value) == nil {
					cache.mu.Lock()
					cache.hits++
					cache.mu.Unlock()
					return value, nil
				}
				cache.mu.Lock()
				if current, exists := cache.entries[key]; exists {
					cache.bytes -= len(current.payload)
					delete(cache.entries, key)
					cache.evictions++
				}
				cache.mu.Unlock()
				continue
			}
			cache.bytes -= len(entry.payload)
			delete(cache.entries, key)
			cache.evictions++
		}
		if !missRecorded {
			cache.misses++
			missRecorded = true
		}
		if call, ok := cache.inflight[key]; ok {
			cache.coalescedWaits++
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
			// Serialize a private copy so proposal normalization cannot mutate
			// the cached value.
			payload, _ = json.Marshal(value)
		}
		cache.mu.Lock()
		if len(payload) > 0 && cache.bytes+len(payload) <= maxCatalogCacheBytes {
			entry := catalogCacheEntry{payload: payload}
			if ttl > 0 {
				entry.expiresAt = cache.now().Add(ttl)
			}
			cache.entries[key] = entry
			cache.bytes += len(payload)
			cache.stores++
		} else if len(payload) > 0 {
			cache.skippedCapacity++
		}
		delete(cache.inflight, key)
		close(call.done)
		cache.mu.Unlock()
		return value, err
	}
}

func (cache *batchCatalog) CardsLimits(ctx context.Context, cabinet CabinetID) (contentapi.CardsLimitsResponse, error) {
	return cachedCatalogReadWithTTL(
		ctx,
		cache,
		cabinet,
		"CardsLimits",
		struct{}{},
		limitsCacheTTL,
		func() (contentapi.CardsLimitsResponse, error) {
			return cache.CatalogTransport.CardsLimits(ctx, cabinet)
		},
		func(value contentapi.CardsLimitsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) Subjects(ctx context.Context, cabinet CabinetID, query contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Subjects", query,
		func() (contentapi.SubjectsResponse, error) {
			return cache.CatalogTransport.Subjects(ctx, cabinet, query)
		},
		func(value contentapi.SubjectsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) SubjectCharacteristics(ctx context.Context, cabinet CabinetID, subjectID int64, query contentapi.SubjectCharacteristicsQuery) (contentapi.SubjectCharacteristicsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "SubjectCharacteristics", struct {
		SubjectID int64
		Query     contentapi.SubjectCharacteristicsQuery
	}{subjectID, query},
		func() (contentapi.SubjectCharacteristicsResponse, error) {
			return cache.CatalogTransport.SubjectCharacteristics(ctx, cabinet, subjectID, query)
		},
		func(value contentapi.SubjectCharacteristicsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) Brands(ctx context.Context, cabinet CabinetID, query contentapi.BrandsQuery) (contentapi.BrandsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Brands", query,
		func() (contentapi.BrandsResponse, error) { return cache.CatalogTransport.Brands(ctx, cabinet, query) },
		func(value contentapi.BrandsResponse) bool { return value.Next >= 0 && value.Total >= 0 },
	)
}

func (cache *batchCatalog) Colors(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryQuery) (contentapi.DirectoryColorsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Colors", query,
		func() (contentapi.DirectoryColorsResponse, error) {
			return cache.CatalogTransport.Colors(ctx, cabinet, query)
		},
		func(value contentapi.DirectoryColorsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) Kinds(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryQuery) (contentapi.DirectoryKindsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Kinds", query,
		func() (contentapi.DirectoryKindsResponse, error) {
			return cache.CatalogTransport.Kinds(ctx, cabinet, query)
		},
		func(value contentapi.DirectoryKindsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) Countries(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryQuery) (contentapi.DirectoryCountriesResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Countries", query,
		func() (contentapi.DirectoryCountriesResponse, error) {
			return cache.CatalogTransport.Countries(ctx, cabinet, query)
		},
		func(value contentapi.DirectoryCountriesResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) Seasons(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryQuery) (contentapi.DirectorySeasonsResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "Seasons", query,
		func() (contentapi.DirectorySeasonsResponse, error) {
			return cache.CatalogTransport.Seasons(ctx, cabinet, query)
		},
		func(value contentapi.DirectorySeasonsResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) VAT(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryQuery) (contentapi.DirectoryVATResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "VAT", query,
		func() (contentapi.DirectoryVATResponse, error) {
			return cache.CatalogTransport.VAT(ctx, cabinet, query)
		},
		func(value contentapi.DirectoryVATResponse) bool { return !value.Error },
	)
}

func (cache *batchCatalog) TNVED(ctx context.Context, cabinet CabinetID, query contentapi.DirectoryTNVEDQuery) (contentapi.DirectoryTNVEDResponse, error) {
	return cachedCatalogRead(ctx, cache, cabinet, "TNVED", query,
		func() (contentapi.DirectoryTNVEDResponse, error) {
			return cache.CatalogTransport.TNVED(ctx, cabinet, query)
		},
		func(value contentapi.DirectoryTNVEDResponse) bool { return !value.Error },
	)
}
