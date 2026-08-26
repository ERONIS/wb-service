package cardpublication_service

import (
	"strings"
	"sync"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type recentVendorKey struct {
	cabinetID CabinetID
	vendor    string
}

type recentNMIDKey struct {
	cabinetID CabinetID
	nmID      int64
}

type recentCardEntry struct {
	card      contentapi.Card
	updatedAt time.Time
}

// RecentCardsCache keeps cards observed by exact vendor-code searches during
// the configured window. It deliberately does not walk the WB catalog: the
// Cards List cursor is pagination state, not a server-side updatedAt filter.
type RecentCardsCache struct {
	mu       sync.RWMutex
	ttl      time.Duration
	byVendor map[recentVendorKey]recentCardEntry
	byNMID   map[recentNMIDKey]recentCardEntry
	now      func() time.Time
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
	cabinetID CabinetID,
	vendorCode string,
) (contentapi.Card, bool) {
	if cache == nil || cabinetID == "" {
		return contentapi.Card{}, false
	}
	key := recentVendorKey{cabinetID: cabinetID, vendor: normalizeCatalogVendorCode(vendorCode)}
	if key.vendor == "" {
		return contentapi.Card{}, false
	}
	cache.mu.RLock()
	entry, ok := cache.byVendor[key]
	cache.mu.RUnlock()
	if !ok || entry.updatedAt.Before(cache.now().UTC().Add(-cache.ttl)) {
		return contentapi.Card{}, false
	}
	return entry.card, true
}

func (cache *RecentCardsCache) LookupMedia(
	cabinetID CabinetID,
	nmID int64,
) (photos int, video bool, ok bool) {
	if cache == nil || cabinetID == "" || nmID <= 0 {
		return 0, false, false
	}
	cache.mu.RLock()
	entry, found := cache.byNMID[recentNMIDKey{cabinetID: cabinetID, nmID: nmID}]
	cache.mu.RUnlock()
	if !found || entry.updatedAt.Before(cache.now().UTC().Add(-cache.ttl)) {
		return 0, false, false
	}
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

func (cache *RecentCardsCache) StoreIfRecent(cabinetID CabinetID, card contentapi.Card) bool {
	if cache == nil || cabinetID == "" || card.NMID <= 0 ||
		normalizeCatalogVendorCode(card.VendorCode) == "" {
		return false
	}
	updatedAt, err := parseWBTime(card.UpdatedAt)
	if err != nil || updatedAt.IsZero() || updatedAt.Before(cache.now().UTC().Add(-cache.ttl)) {
		return false
	}
	cache.store(cabinetID, card, updatedAt)
	return true
}

func (cache *RecentCardsCache) store(
	cabinetID CabinetID,
	card contentapi.Card,
	updatedAt time.Time,
) {
	vendorKey := recentVendorKey{
		cabinetID: cabinetID,
		vendor:    normalizeCatalogVendorCode(card.VendorCode),
	}
	nmIDKey := recentNMIDKey{cabinetID: cabinetID, nmID: card.NMID}
	entry := recentCardEntry{card: card, updatedAt: updatedAt.UTC()}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if previous, ok := cache.byVendor[vendorKey]; ok && previous.card.NMID != card.NMID {
		delete(cache.byNMID, recentNMIDKey{cabinetID: cabinetID, nmID: previous.card.NMID})
	}
	if previous, ok := cache.byNMID[nmIDKey]; ok &&
		normalizeCatalogVendorCode(previous.card.VendorCode) != vendorKey.vendor {
		delete(cache.byVendor, recentVendorKey{
			cabinetID: cabinetID,
			vendor:    normalizeCatalogVendorCode(previous.card.VendorCode),
		})
	}
	cache.byVendor[vendorKey] = entry
	cache.byNMID[nmIDKey] = entry
}
