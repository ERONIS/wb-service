package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	wb_catalog "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1/catalog"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	maxVendorSearchPages        = 100
	bulkVendorCodeScanThreshold = 5
	normalCatalogProbePages     = 1
	recentObservationTTL        = 30 * time.Second
)

var ErrCatalogChangedDuringScan = wb_catalog.ErrCursorStalled

type CatalogObservation struct {
	TargetID   int64
	SellerID   SellerID
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
		logger:    core_logger.Fallback(loggers...),
	}
}

func (reader *CatalogReader) WaitForMedia(
	ctx context.Context,
	sellerID SellerID,
	vendorCode string,
	nmID int64,
	expectedPhotos int,
	expectedVideo bool,
	interval time.Duration,
	timeout time.Duration,
) (err error) {
	lastPhotosCount, lastCardFound := 0, false
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || sellerID == "" || vendorCode == "" || nmID <= 0 ||
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
		cards, err := reader.readNormalVendorCode(ctx, sellerID, vendorCode)
		if err != nil {
			return false, err
		}
		lastPhotosCount, lastCardFound = 0, false
		for _, card := range cards {
			if card.NMID == nmID {
				lastCardFound = true
				lastPhotosCount = loadedPhotoCount(card.Photos)
			}
			reader.recent.StoreIfRecent(sellerID, card)
			if mediaLoaded(card) {
				return true, nil
			}
		}
		return false, nil
	}

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

func (reader *CatalogReader) ReadActiveVendorCode(
	ctx context.Context,
	targetID int64,
	sellerID SellerID,
	vendorCode string,
) (observation CatalogObservation, err error) {
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || targetID <= 0 || sellerID == "" || vendorCode == "" {
		return CatalogObservation{}, errors.New("active publication catalog query is invalid")
	}
	cards := make([]contentapi.Card, 0, 1)
	if card, ok := reader.recent.LookupVendorCode(sellerID, vendorCode); ok {
		cards = append(cards, card)
	} else {
		found, err := reader.readNormalVendorCode(ctx, sellerID, vendorCode)
		if err != nil {
			return CatalogObservation{}, err
		}
		for _, card := range found {
			reader.recent.StoreIfRecent(sellerID, card)
		}
		cards = found
	}
	return CatalogObservation{
		TargetID:   targetID,
		SellerID:   sellerID,
		Normal:     cards,
		ObservedAt: time.Now().UTC(),
	}, nil
}

func (reader *CatalogReader) ReadVendorCodes(
	ctx context.Context,
	targetID int64,
	sellerID SellerID,
	vendorCodes []string,
) (observation CatalogObservation, err error) {
	if ctx == nil || targetID <= 0 || sellerID == "" {
		return CatalogObservation{}, errors.New("publication catalog query is invalid")
	}
	vendorCodes = normalizedUniqueStrings(vendorCodes)
	if len(vendorCodes) == 0 {
		return CatalogObservation{}, errors.New("publication vendor-code query is empty")
	}

	normalByID := make(map[int64]contentapi.Card)
	missingNormal := make([]string, 0, len(vendorCodes))
	for _, vendorCode := range vendorCodes {
		if card, ok := reader.recent.LookupVendorCode(sellerID, vendorCode); ok {
			normalByID[card.NMID] = card
		} else {
			missingNormal = append(missingNormal, vendorCode)
		}
	}
	var normalCards []contentapi.Card
	var trashCards []contentapi.TrashCard
	group, readCtx := errgroup.WithContext(ctx)
	if len(missingNormal) > 0 {
		group.Go(func() error {
			var err error
			normalCards, _, _, err = reader.readNormalVendorCodes(
				readCtx,
				sellerID,
				missingNormal,
			)
			return err
		})
	}
	group.Go(func() error {
		var err error
		trashCards, _, _, err = reader.readTrashVendorCodes(
			readCtx,
			sellerID,
			vendorCodes,
		)
		return err
	})
	if err := group.Wait(); err != nil {
		return CatalogObservation{}, err
	}
	for _, card := range normalCards {
		normalByID[card.NMID] = card
		reader.recent.StoreIfRecent(sellerID, card)
	}
	trashByID := make(map[int64]contentapi.TrashCard, len(trashCards))
	for _, card := range trashCards {
		trashByID[card.NMID] = card
	}

	observation = CatalogObservation{
		TargetID:   targetID,
		SellerID:   sellerID,
		Normal:     sortedNormalCards(normalByID),
		Trash:      sortedTrashCards(trashByID),
		ObservedAt: time.Now().UTC(),
	}
	return observation, nil
}

func (reader *CatalogReader) readNormalVendorCodes(
	ctx context.Context,
	sellerID SellerID,
	vendorCodes []string,
) ([]contentapi.Card, int, bool, error) {
	if len(vendorCodes) >= bulkVendorCodeScanThreshold {
		cards, requests, complete, err := reader.scanNormalVendorCodes(
			ctx,
			sellerID,
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
			sellerID,
			vendorCodes,
		)
		return result, requests + exactRequests, false, err
	}
	result, requests, err := reader.searchNormalVendorCodes(ctx, sellerID, vendorCodes)
	return result, requests, false, err
}

func (reader *CatalogReader) readTrashVendorCodes(
	ctx context.Context,
	sellerID SellerID,
	vendorCodes []string,
) ([]contentapi.TrashCard, int, bool, error) {
	if len(vendorCodes) >= bulkVendorCodeScanThreshold {
		cards, requests, complete, err := reader.scanTrashVendorCodes(
			ctx,
			sellerID,
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
			sellerID,
			vendorCodes,
		)
		return result, requests + exactRequests, false, err
	}
	result, requests, err := reader.searchTrashVendorCodes(ctx, sellerID, vendorCodes)
	return result, requests, false, err
}

func (reader *CatalogReader) searchNormalVendorCodes(
	ctx context.Context,
	sellerID SellerID,
	vendorCodes []string,
) ([]contentapi.Card, int, error) {
	result := make(map[int64]contentapi.Card)
	requests := 0
	for _, vendorCode := range vendorCodes {
		cards, count, err := reader.readNormalVendorCodeCount(ctx, sellerID, vendorCode)
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
	sellerID SellerID,
	vendorCodes []string,
) ([]contentapi.TrashCard, int, error) {
	result := make(map[int64]contentapi.TrashCard)
	requests := 0
	for _, vendorCode := range vendorCodes {
		cards, count, err := reader.readTrashVendorCodeCount(ctx, sellerID, vendorCode)
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

func (reader *CatalogReader) scanNormalVendorCodes(ctx context.Context, sellerID SellerID, vendorCodes []string) ([]contentapi.Card, int, bool, error) {
	wanted := stringSet(vendorCodes)
	result := make(map[int64]contentapi.Card)
	pages, err := wb_catalog.WalkCards(ctx, reader.transport, sellerID, contentapi.CardsListQuery{}, contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{Sort: contentapi.CardsSort{Ascending: true}}}, normalCatalogProbePages, func(card contentapi.Card) bool {
		if _, ok := wanted[card.VendorCode]; ok {
			result[card.NMID] = card
		}
		return true
	})
	if errors.Is(err, wb_catalog.ErrPageLimit) {
		return nil, pages, false, nil
	}
	if err != nil {
		return nil, pages, false, err
	}
	return sortedNormalCards(result), pages, true, nil
}

func (reader *CatalogReader) scanTrashVendorCodes(ctx context.Context, sellerID SellerID, vendorCodes []string) ([]contentapi.TrashCard, int, bool, error) {
	wanted := stringSet(vendorCodes)
	result := make(map[int64]contentapi.TrashCard)
	pages, err := wb_catalog.WalkTrashCards(ctx, reader.transport, sellerID, contentapi.TrashCardsListQuery{}, contentapi.TrashCardsListRequest{Settings: contentapi.TrashCardsListSettings{Sort: contentapi.CardsSort{Ascending: true}}}, min(len(vendorCodes), maxVendorSearchPages), func(card contentapi.TrashCard) bool {
		if _, ok := wanted[card.VendorCode]; ok {
			result[card.NMID] = card
		}
		return true
	})
	if errors.Is(err, wb_catalog.ErrPageLimit) {
		return nil, pages, false, nil
	}
	if err != nil {
		return nil, pages, false, err
	}
	return sortedTrashCards(result), pages, true, nil
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
	sellerID SellerID,
	vendorCode string,
) ([]contentapi.Card, error) {
	cards, _, err := reader.readNormalVendorCodeCount(ctx, sellerID, vendorCode)
	return cards, err
}

func (reader *CatalogReader) readNormalVendorCodeCount(ctx context.Context, sellerID SellerID, vendorCode string) ([]contentapi.Card, int, error) {
	cards, pages, err := wb_catalog.FindByVendorCode(ctx, reader.transport, sellerID, vendorCode, maxVendorSearchPages)
	if err != nil {
		return nil, pages, err
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].NMID < cards[j].NMID })
	return cards, pages, nil
}

func (reader *CatalogReader) readTrashVendorCode(
	ctx context.Context,
	sellerID SellerID,
	vendorCode string,
) ([]contentapi.TrashCard, error) {
	cards, _, err := reader.readTrashVendorCodeCount(ctx, sellerID, vendorCode)
	return cards, err
}

func (reader *CatalogReader) readTrashVendorCodeCount(ctx context.Context, sellerID SellerID, vendorCode string) ([]contentapi.TrashCard, int, error) {
	result := make(map[int64]contentapi.TrashCard)
	pages, err := wb_catalog.WalkTrashCards(ctx, reader.transport, sellerID, contentapi.TrashCardsListQuery{}, contentapi.TrashCardsListRequest{Settings: contentapi.TrashCardsListSettings{
		Sort: contentapi.CardsSort{Ascending: true}, Filter: &contentapi.TrashCardsListFilter{TextSearch: vendorCode},
	}}, maxVendorSearchPages, func(card contentapi.TrashCard) bool {
		if card.VendorCode == vendorCode {
			result[card.NMID] = card
		}
		return true
	})
	if err != nil {
		return nil, pages, err
	}
	return sortedTrashCards(result), pages, nil
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
		SellerID:    observation.SellerID,
		Digest:      digestParts("cardpublication-observation:v1", payload),
		NormalCount: len(observation.Normal),
		TrashCount:  len(observation.Trash),
		Payload:     payload,
		ObservedAt:  observation.ObservedAt,
	}, nil
}
