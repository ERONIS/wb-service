package catalog

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type recentVendorKey struct {
	sellerID core_domain.SellerID
	vendor   string
}

type recentNMIDKey struct {
	sellerID core_domain.SellerID
	nmID     int64
}

type recentCardEntry struct {
	card       contentapi.Card
	observedAt time.Time
}

type RecentCardsCache struct {
	mu           sync.RWMutex
	ttl          time.Duration
	byVendor     map[recentVendorKey]recentCardEntry
	byNMID       map[recentNMIDKey]recentCardEntry
	vendorHits   atomic.Uint64
	vendorMisses atomic.Uint64
	mediaHits    atomic.Uint64
	mediaMisses  atomic.Uint64
	stores       atomic.Uint64
	now          func() time.Time
}

type RecentCardsCacheStats struct {
	VendorEntries int
	NMIDEntries   int
	VendorHits    uint64
	VendorMisses  uint64
	MediaHits     uint64
	MediaMisses   uint64
	Stores        uint64
}

func (cache *RecentCardsCache) Stats() RecentCardsCacheStats {
	if cache == nil {
		return RecentCardsCacheStats{}
	}
	cache.mu.RLock()
	vendorEntries, nmIDEntries := len(cache.byVendor), len(cache.byNMID)
	cache.mu.RUnlock()
	return RecentCardsCacheStats{
		VendorEntries: vendorEntries,
		NMIDEntries:   nmIDEntries,
		VendorHits:    cache.vendorHits.Load(),
		VendorMisses:  cache.vendorMisses.Load(),
		MediaHits:     cache.mediaHits.Load(),
		MediaMisses:   cache.mediaMisses.Load(),
		Stores:        cache.stores.Load(),
	}
}

func NewRecentCardsCache(ttl time.Duration) *RecentCardsCache {
	if ttl <= 0 {
		panic("recent cards cache TTL must be positive")
	}
	return &RecentCardsCache{
		ttl:      ttl,
		byVendor: make(map[recentVendorKey]recentCardEntry),
		byNMID:   make(map[recentNMIDKey]recentCardEntry),
		now:      time.Now,
	}
}

func normalizeCatalogVendorCode(value string) string {
	return strings.TrimSpace(value)
}

func (cache *RecentCardsCache) LookupVendorCode(
	sellerID core_domain.SellerID,
	vendorCode string,
) (contentapi.Card, bool) {
	if cache == nil || sellerID == "" {
		return contentapi.Card{}, false
	}
	key := recentVendorKey{sellerID: sellerID, vendor: normalizeCatalogVendorCode(vendorCode)}
	if key.vendor == "" {
		return contentapi.Card{}, false
	}
	cache.mu.RLock()
	entry, ok := cache.byVendor[key]
	cache.mu.RUnlock()
	if !ok || entry.observedAt.Before(cache.now().UTC().Add(-cache.ttl)) {
		cache.vendorMisses.Add(1)
		return contentapi.Card{}, false
	}
	cache.vendorHits.Add(1)
	return entry.card, true
}

func (cache *RecentCardsCache) LookupMedia(
	sellerID core_domain.SellerID,
	nmID int64,
) (photos int, video bool, ok bool) {
	if cache == nil || sellerID == "" || nmID <= 0 {
		return 0, false, false
	}
	cache.mu.RLock()
	entry, found := cache.byNMID[recentNMIDKey{sellerID: sellerID, nmID: nmID}]
	cache.mu.RUnlock()
	if !found || entry.observedAt.Before(cache.now().UTC().Add(-cache.ttl)) {
		cache.mediaMisses.Add(1)
		return 0, false, false
	}
	cache.mediaHits.Add(1)
	return loadedPhotoCount(entry.card.Photos), strings.TrimSpace(entry.card.Video) != "", true
}

func loadedPhotoCount(photos []contentapi.CardPhoto) int {
	count := 0
	for _, photo := range photos {
		if strings.TrimSpace(photo.Big) != "" || strings.TrimSpace(photo.C516x688) != "" ||
			strings.TrimSpace(photo.C246x328) != "" || strings.TrimSpace(photo.Square) != "" ||
			strings.TrimSpace(photo.TM) != "" {
			count++
		}
	}
	return count
}

func (cache *RecentCardsCache) StoreIfRecent(sellerID core_domain.SellerID, card contentapi.Card) bool {
	if cache == nil || sellerID == "" || card.NMID <= 0 ||
		normalizeCatalogVendorCode(card.VendorCode) == "" {
		return false
	}
	cache.store(sellerID, card, cache.now().UTC())
	return true
}

func (cache *RecentCardsCache) store(
	sellerID core_domain.SellerID,
	card contentapi.Card,
	observedAt time.Time,
) {
	vendorKey := recentVendorKey{
		sellerID: sellerID,
		vendor:   normalizeCatalogVendorCode(card.VendorCode),
	}
	nmIDKey := recentNMIDKey{sellerID: sellerID, nmID: card.NMID}
	entry := recentCardEntry{card: card, observedAt: observedAt.UTC()}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if previous, ok := cache.byVendor[vendorKey]; ok && previous.card.NMID != card.NMID {
		delete(cache.byNMID, recentNMIDKey{sellerID: sellerID, nmID: previous.card.NMID})
	}
	if previous, ok := cache.byNMID[nmIDKey]; ok &&
		normalizeCatalogVendorCode(previous.card.VendorCode) != vendorKey.vendor {
		delete(cache.byVendor, recentVendorKey{
			sellerID: sellerID,
			vendor:   normalizeCatalogVendorCode(previous.card.VendorCode),
		})
	}
	cache.byVendor[vendorKey] = entry
	cache.byNMID[nmIDKey] = entry
	cache.stores.Add(1)
}
