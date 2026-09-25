package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	maxVendorSearchPages        = 100
	bulkVendorCodeScanThreshold = 5
	normalCatalogProbePages     = 1
	recentObservationTTL        = 30 * time.Second
)

var ErrCatalogChangedDuringScan = fmt.Errorf(
	"WB card catalog changed during scan: %w",
	core_errors.ErrConflict,
)

type CatalogObservation struct {
	TargetID   int64
	CabinetID  CabinetID
	Normal     []contentapi.Card
	Trash      []contentapi.TrashCard
	ObservedAt time.Time
}

type catalogPayload struct {
	Normal []contentapi.Card      `json:"normal"`
	Trash  []contentapi.TrashCard `json:"trash"`
}

type CatalogReader struct {
	transport CatalogTransport
	recent    *RecentCardsCache
	logger    *zap.Logger
}

func NewCatalogReader(transport CatalogTransport, loggers ...*zap.Logger) *CatalogReader {
	if transport == nil {
		panic("cardpublication catalog transport is nil")
	}
	return &CatalogReader{
		transport: transport,
		recent:    NewRecentCardsCache(recentObservationTTL),
		logger:    core_observability.Logger(loggers...),
	}
}

func (reader *CatalogReader) WaitForMedia(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
	nmID int64,
	expectedPhotos int,
	expectedVideo bool,
	interval time.Duration,
	timeout time.Duration,
) (err error) {
	startedAt := time.Now()
	logger := core_observability.LoggerWithContext(ctx, reader.logger)
	core_observability.LogStarted(logger, "cardpublication", "wait_for_media_visibility",
		zap.String("cabinet_id", string(cabinetID)), zap.Int64("nm_id", nmID),
		zap.Duration("check_interval", interval), zap.Duration("check_timeout", timeout))
	pollsCount := 0
	lastPhotosCount, lastCardFound, lastVideoVisible := 0, false, false
	defer func() {
		core_observability.LogTiming(
			logger,
			"cardpublication",
			"wait_for_media_visibility",
			startedAt,
			err,
			zap.String("cabinet_id", string(cabinetID)),
			zap.Int64("nm_id", nmID),
			zap.Int("expected_photos_count", expectedPhotos),
			zap.Bool("expected_video", expectedVideo),
			zap.Int("polls_count", pollsCount),
			zap.Bool("last_card_found", lastCardFound),
			zap.Int("last_photos_count", lastPhotosCount),
			zap.Bool("last_video_visible", lastVideoVisible),
		)
	}()
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || cabinetID == "" || vendorCode == "" || nmID <= 0 ||
		expectedPhotos < 0 || interval <= 0 || timeout <= 0 {
		return errors.New("targeted WB media check is invalid")
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mediaLoaded := func(card contentapi.Card) bool {
		return card.NMID == nmID && loadedPhotoCount(card.Photos) >= expectedPhotos &&
			(!expectedVideo || strings.TrimSpace(card.Video) != "")
	}
	refresh := func() (bool, error) {
		pollsCount++
		cards, err := reader.readNormalVendorCode(ctx, cabinetID, vendorCode)
		if err != nil {
			return false, err
		}
		lastPhotosCount, lastCardFound, lastVideoVisible = 0, false, false
		for _, card := range cards {
			if card.NMID == nmID {
				lastCardFound = true
				lastPhotosCount = loadedPhotoCount(card.Photos)
				lastVideoVisible = strings.TrimSpace(card.Video) != ""
			}
			reader.recent.StoreIfRecent(cabinetID, card)
			if mediaLoaded(card) {
				return true, nil
			}
		}
		return false, nil
	}

	// Apply the deadline to both requests and limiter waits. Space out repeated
	// negative observations so missing media cannot monopolize the cards bucket.
	delay := interval
	maxDelay := max(interval, 30*time.Second)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var lastErr error
	for {
		if ctx.Err() != nil {
			if parentCtx.Err() != nil {
				return parentCtx.Err()
			}
			return fmt.Errorf("WB media did not become visible for vendorCode %q (card_found=%t, photos=%d/%d): %w",
				vendorCode, lastCardFound, lastPhotosCount, expectedPhotos, errors.Join(ctx.Err(), lastErr))
		}
		loaded, err := refresh()
		if loaded {
			return nil
		}
		lastErr = err
		timer.Reset(delay)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		if delay < maxDelay {
			delay = min(delay*2, maxDelay)
		}
	}
}

// ReadActiveVendorCode observes one exact active card without querying trash.
// Media dispatch uses it after product reconciliation has already established
// that this vendorCode belongs to the active catalog.
func (reader *CatalogReader) ReadActiveVendorCode(
	ctx context.Context,
	targetID int64,
	cabinetID CabinetID,
	vendorCode string,
) (observation CatalogObservation, err error) {
	startedAt := time.Now()
	logger := core_observability.LoggerWithContext(ctx, reader.logger)
	cacheHit := false
	defer func() {
		cacheStats := reader.recent.Stats()
		core_observability.LogTiming(
			logger,
			"cardpublication",
			"read_active_vendor_code",
			startedAt,
			err,
			zap.Int64("target_id", targetID),
			zap.String("cabinet_id", string(cabinetID)),
			zap.Bool("cache_hit", cacheHit),
			zap.Int("normal_cards_count", len(observation.Normal)),
			zap.Int("recent_cache_vendor_entries", cacheStats.VendorEntries),
			zap.Uint64("recent_cache_vendor_hits", cacheStats.VendorHits),
			zap.Uint64("recent_cache_vendor_misses", cacheStats.VendorMisses),
			zap.Uint64("recent_cache_stores", cacheStats.Stores),
		)
	}()
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || targetID <= 0 || cabinetID == "" || vendorCode == "" {
		return CatalogObservation{}, errors.New("active publication catalog query is invalid")
	}
	cards := make([]contentapi.Card, 0, 1)
	if card, ok := reader.recent.LookupVendorCode(cabinetID, vendorCode); ok {
		cacheHit = true
		cards = append(cards, card)
	} else {
		found, err := reader.readNormalVendorCode(ctx, cabinetID, vendorCode)
		if err != nil {
			return CatalogObservation{}, err
		}
		for _, card := range found {
			reader.recent.StoreIfRecent(cabinetID, card)
		}
		cards = found
	}
	return CatalogObservation{
		TargetID:   targetID,
		CabinetID:  cabinetID,
		Normal:     cards,
		ObservedAt: time.Now().UTC(),
	}, nil
}

// ReadVendorCodes reads only exact vendor-code matches. The recent-cards cache
// is the first source for active cards; cache misses are resolved with WB
// textSearch. Trash is always queried by vendorCode because it has no updated
// cards feed.
func (reader *CatalogReader) ReadVendorCodes(
	ctx context.Context,
	targetID int64,
	cabinetID CabinetID,
	vendorCodes []string,
) (observation CatalogObservation, err error) {
	startedAt := time.Now()
	logger := core_observability.LoggerWithContext(ctx, reader.logger)
	core_observability.LogStarted(logger, "cardpublication", "read_vendor_codes",
		zap.String("cabinet_id", string(cabinetID)), zap.Int64("target_id", targetID),
		zap.Int("requested_vendor_codes_count", len(vendorCodes)))
	var normalReadDuration, trashReadDuration time.Duration
	cacheHits, normalReads := 0, 0
	normalRequests, trashRequests := 0, 0
	bulkNormal, bulkTrash := false, false
	requestedVendorCodes := len(vendorCodes)
	defer func() {
		cacheStats := reader.recent.Stats()
		core_observability.LogTiming(
			logger,
			"cardpublication",
			"read_vendor_codes",
			startedAt,
			err,
			zap.Int64("target_id", targetID),
			zap.String("cabinet_id", string(cabinetID)),
			zap.Int("requested_vendor_codes_count", requestedVendorCodes),
			zap.Int("vendor_codes_count", len(vendorCodes)),
			zap.Int("cache_hits_count", cacheHits),
			zap.Int("normal_reads_count", normalReads),
			zap.Int("normal_requests_count", normalRequests),
			zap.Int("trash_requests_count", trashRequests),
			zap.Bool("bulk_normal_scan", bulkNormal),
			zap.Bool("bulk_trash_scan", bulkTrash),
			zap.Int("normal_cards_count", len(observation.Normal)),
			zap.Int("trash_cards_count", len(observation.Trash)),
			zap.Int("recent_cache_vendor_entries", cacheStats.VendorEntries),
			zap.Uint64("recent_cache_vendor_hits", cacheStats.VendorHits),
			zap.Uint64("recent_cache_vendor_misses", cacheStats.VendorMisses),
			zap.Uint64("recent_cache_stores", cacheStats.Stores),
			zap.Duration("normal_reads_duration", normalReadDuration),
			zap.Duration("trash_reads_duration", trashReadDuration),
		)
	}()
	if ctx == nil || targetID <= 0 || cabinetID == "" {
		return CatalogObservation{}, errors.New("publication catalog query is invalid")
	}
	vendorCodes = normalizedUniqueStrings(vendorCodes)
	if len(vendorCodes) == 0 {
		return CatalogObservation{}, errors.New("publication vendor-code query is empty")
	}

	normalByID := make(map[int64]contentapi.Card)
	missingNormal := make([]string, 0, len(vendorCodes))
	for _, vendorCode := range vendorCodes {
		if card, ok := reader.recent.LookupVendorCode(cabinetID, vendorCode); ok {
			cacheHits++
			normalByID[card.NMID] = card
		} else {
			missingNormal = append(missingNormal, vendorCode)
		}
	}
	normalReads = len(missingNormal)
	var normalCards []contentapi.Card
	var trashCards []contentapi.TrashCard
	group, readCtx := errgroup.WithContext(ctx)
	if len(missingNormal) > 0 {
		group.Go(func() error {
			startedAt := time.Now()
			var err error
			normalCards, normalRequests, bulkNormal, err = reader.readNormalVendorCodes(
				readCtx,
				cabinetID,
				missingNormal,
			)
			normalReadDuration = time.Since(startedAt)
			return err
		})
	}
	group.Go(func() error {
		startedAt := time.Now()
		var err error
		trashCards, trashRequests, bulkTrash, err = reader.readTrashVendorCodes(
			readCtx,
			cabinetID,
			vendorCodes,
		)
		trashReadDuration = time.Since(startedAt)
		return err
	})
	if err := group.Wait(); err != nil {
		return CatalogObservation{}, err
	}
	for _, card := range normalCards {
		normalByID[card.NMID] = card
		reader.recent.StoreIfRecent(cabinetID, card)
	}
	trashByID := make(map[int64]contentapi.TrashCard, len(trashCards))
	for _, card := range trashCards {
		trashByID[card.NMID] = card
	}

	observation = CatalogObservation{
		TargetID:   targetID,
		CabinetID:  cabinetID,
		Normal:     sortedNormalCards(normalByID),
		Trash:      sortedTrashCards(trashByID),
		ObservedAt: time.Now().UTC(),
	}
	return observation, nil
}

// readNormalVendorCodes uses one full catalog scan when the first page proves
// that the catalog is small. A full first page falls back immediately to exact
// searches: cursor.total is the page size, so scanning N speculative pages and
// then issuing N exact searches only doubles work for large cabinets.
func (reader *CatalogReader) readNormalVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.Card, int, bool, error) {
	if len(vendorCodes) >= bulkVendorCodeScanThreshold {
		cards, requests, complete, err := reader.scanNormalVendorCodes(
			ctx,
			cabinetID,
			vendorCodes,
		)
		if err != nil {
			return nil, requests, true, err
		}
		if complete {
			return cards, requests, true, nil
		}
		result, exactRequests, err := reader.searchNormalVendorCodes(
			ctx,
			cabinetID,
			vendorCodes,
		)
		return result, requests + exactRequests, false, err
	}
	result, requests, err := reader.searchNormalVendorCodes(ctx, cabinetID, vendorCodes)
	return result, requests, false, err
}

func (reader *CatalogReader) readTrashVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.TrashCard, int, bool, error) {
	if len(vendorCodes) >= bulkVendorCodeScanThreshold {
		cards, requests, complete, err := reader.scanTrashVendorCodes(
			ctx,
			cabinetID,
			vendorCodes,
		)
		if err != nil {
			return nil, requests, true, err
		}
		if complete {
			return cards, requests, true, nil
		}
		result, exactRequests, err := reader.searchTrashVendorCodes(
			ctx,
			cabinetID,
			vendorCodes,
		)
		return result, requests + exactRequests, false, err
	}
	result, requests, err := reader.searchTrashVendorCodes(ctx, cabinetID, vendorCodes)
	return result, requests, false, err
}

func (reader *CatalogReader) searchNormalVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.Card, int, error) {
	result := make(map[int64]contentapi.Card)
	requests := 0
	for _, vendorCode := range vendorCodes {
		cards, count, err := reader.readNormalVendorCodeCount(ctx, cabinetID, vendorCode)
		requests += count
		if err != nil {
			return nil, requests, err
		}
		for _, card := range cards {
			result[card.NMID] = card
		}
	}
	return sortedNormalCards(result), requests, nil
}

func (reader *CatalogReader) searchTrashVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.TrashCard, int, error) {
	result := make(map[int64]contentapi.TrashCard)
	requests := 0
	for _, vendorCode := range vendorCodes {
		cards, count, err := reader.readTrashVendorCodeCount(ctx, cabinetID, vendorCode)
		requests += count
		if err != nil {
			return nil, requests, err
		}
		for _, card := range cards {
			result[card.NMID] = card
		}
	}
	return sortedTrashCards(result), requests, nil
}

func (reader *CatalogReader) scanNormalVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.Card, int, bool, error) {
	wanted := stringSet(vendorCodes)
	result := make(map[int64]contentapi.Card)
	cursor := contentapi.CardsListCursor{Limit: contentapi.MaxCardsListPageSize}
	for page := 0; page < normalCatalogProbePages; page++ {
		response, err := reader.transport.CardsList(
			ctx,
			cabinetID,
			contentapi.CardsListQuery{},
			contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Cursor: cursor,
			}},
		)
		requests := page + 1
		if err != nil {
			return nil, requests, false, fmt.Errorf("scan WB normal cards: %w", err)
		}
		for _, card := range response.Cards {
			if _, ok := wanted[card.VendorCode]; ok {
				result[card.NMID] = card
			}
		}
		// cursor.total describes this page, not the size of the catalog.
		if len(response.Cards) < cursor.Limit {
			return sortedNormalCards(result), requests, true, nil
		}
		next := contentapi.CardsListCursor{
			UpdatedAt: response.Cursor.UpdatedAt,
			NMID:      response.Cursor.NMID,
			Limit:     cursor.Limit,
		}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return nil, requests, false, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, normalCatalogProbePages, false, nil
}

func (reader *CatalogReader) scanTrashVendorCodes(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCodes []string,
) ([]contentapi.TrashCard, int, bool, error) {
	wanted := stringSet(vendorCodes)
	result := make(map[int64]contentapi.TrashCard)
	cursor := contentapi.TrashCardsListCursor{Limit: contentapi.MaxTrashCardsListPageSize}
	for page := 0; page < len(vendorCodes) && page < maxVendorSearchPages; page++ {
		response, err := reader.transport.TrashCardsList(
			ctx,
			cabinetID,
			contentapi.TrashCardsListQuery{},
			contentapi.TrashCardsListRequest{Settings: contentapi.TrashCardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Cursor: cursor,
			}},
		)
		requests := page + 1
		if err != nil {
			return nil, requests, false, fmt.Errorf("scan WB trash cards: %w", err)
		}
		for _, card := range response.Cards {
			if _, ok := wanted[card.VendorCode]; ok {
				result[card.NMID] = card
			}
		}
		if len(response.Cards) < cursor.Limit {
			return sortedTrashCards(result), requests, true, nil
		}
		next := contentapi.TrashCardsListCursor{
			TrashedAt: response.Cursor.TrashedAt,
			NMID:      response.Cursor.NMID,
			Limit:     cursor.Limit,
		}
		if next.TrashedAt == cursor.TrashedAt && next.NMID == cursor.NMID {
			return nil, requests, false, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, min(len(vendorCodes), maxVendorSearchPages), false, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func (reader *CatalogReader) readNormalVendorCode(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.Card, error) {
	cards, _, err := reader.readNormalVendorCodeCount(ctx, cabinetID, vendorCode)
	return cards, err
}

func (reader *CatalogReader) readNormalVendorCodeCount(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.Card, int, error) {
	cursor := contentapi.CardsListCursor{Limit: contentapi.MaxCardsListPageSize}
	cardsByID := make(map[int64]contentapi.Card)
	for page := 0; page < maxVendorSearchPages; page++ {
		response, err := reader.transport.CardsList(
			ctx,
			cabinetID,
			contentapi.CardsListQuery{},
			contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Filter: &contentapi.CardsListFilter{TextSearch: vendorCode},
				Cursor: cursor,
			}},
		)
		if err != nil {
			return nil, page + 1, fmt.Errorf("search WB normal card by vendorCode: %w", err)
		}
		for _, card := range response.Cards {
			if card.VendorCode == vendorCode {
				cardsByID[card.NMID] = card
			}
		}
		if len(response.Cards) < contentapi.MaxCardsListPageSize {
			return sortedNormalCards(cardsByID), page + 1, nil
		}
		next := contentapi.CardsListCursor{
			UpdatedAt: response.Cursor.UpdatedAt,
			NMID:      response.Cursor.NMID,
			Limit:     contentapi.MaxCardsListPageSize,
		}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return nil, page + 1, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, maxVendorSearchPages, errors.New("WB normal vendor-code search pagination limit exceeded")
}

func (reader *CatalogReader) readTrashVendorCode(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.TrashCard, error) {
	cards, _, err := reader.readTrashVendorCodeCount(ctx, cabinetID, vendorCode)
	return cards, err
}

func (reader *CatalogReader) readTrashVendorCodeCount(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.TrashCard, int, error) {
	cursor := contentapi.TrashCardsListCursor{Limit: contentapi.MaxTrashCardsListPageSize}
	cardsByID := make(map[int64]contentapi.TrashCard)
	for page := 0; page < maxVendorSearchPages; page++ {
		response, err := reader.transport.TrashCardsList(
			ctx,
			cabinetID,
			contentapi.TrashCardsListQuery{},
			contentapi.TrashCardsListRequest{Settings: contentapi.TrashCardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Filter: &contentapi.TrashCardsListFilter{TextSearch: vendorCode},
				Cursor: cursor,
			}},
		)
		if err != nil {
			return nil, page + 1, fmt.Errorf("search WB trash card by vendorCode: %w", err)
		}
		for _, card := range response.Cards {
			if card.VendorCode == vendorCode {
				cardsByID[card.NMID] = card
			}
		}
		if len(response.Cards) < contentapi.MaxTrashCardsListPageSize {
			return sortedTrashCards(cardsByID), page + 1, nil
		}
		next := contentapi.TrashCardsListCursor{
			TrashedAt: response.Cursor.TrashedAt,
			NMID:      response.Cursor.NMID,
			Limit:     contentapi.MaxTrashCardsListPageSize,
		}
		if next.TrashedAt == cursor.TrashedAt && next.NMID == cursor.NMID {
			return nil, page + 1, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, maxVendorSearchPages, errors.New("WB trash vendor-code search pagination limit exceeded")
}

func sortedNormalCards(cardsByID map[int64]contentapi.Card) []contentapi.Card {
	cards := make([]contentapi.Card, 0, len(cardsByID))
	for _, card := range cardsByID {
		cards = append(cards, card)
	}
	sort.Slice(cards, func(left, right int) bool {
		if cards[left].VendorCode != cards[right].VendorCode {
			return cards[left].VendorCode < cards[right].VendorCode
		}
		return cards[left].NMID < cards[right].NMID
	})
	return cards
}

func sortedTrashCards(cardsByID map[int64]contentapi.TrashCard) []contentapi.TrashCard {
	cards := make([]contentapi.TrashCard, 0, len(cardsByID))
	for _, card := range cardsByID {
		cards = append(cards, card)
	}
	sort.Slice(cards, func(left, right int) bool {
		if cards[left].VendorCode != cards[right].VendorCode {
			return cards[left].VendorCode < cards[right].VendorCode
		}
		return cards[left].NMID < cards[right].NMID
	})
	return cards
}

func DraftObservation(observation CatalogObservation) (ObservationDraft, error) {
	payload, err := json.Marshal(catalogPayload{
		Normal: observation.Normal,
		Trash:  observation.Trash,
	})
	if err != nil {
		return ObservationDraft{}, fmt.Errorf("encode publication observation: %w", err)
	}
	return ObservationDraft{
		TargetID:    observation.TargetID,
		CabinetID:   observation.CabinetID,
		Digest:      digestParts("cardpublication-observation:v1", payload),
		NormalCount: len(observation.Normal),
		TrashCount:  len(observation.Trash),
		Payload:     payload,
		ObservedAt:  observation.ObservedAt,
	}, nil
}
