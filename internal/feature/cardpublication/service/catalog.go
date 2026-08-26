package cardpublication_service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

const (
	maxVendorSearchPages = 100
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
}

func NewCatalogReader(transport CatalogTransport) *CatalogReader {
	if transport == nil {
		panic("cardpublication catalog transport is nil")
	}
	return &CatalogReader{
		transport: transport,
		recent:    NewRecentCardsCache(time.Hour),
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
) error {
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || cabinetID == "" || vendorCode == "" || nmID <= 0 ||
		expectedPhotos < 0 || interval <= 0 || timeout <= 0 {
		return errors.New("targeted WB media check is invalid")
	}
	mediaLoaded := func(card contentapi.Card) bool {
		return card.NMID == nmID && loadedPhotoCount(card.Photos) >= expectedPhotos &&
			(!expectedVideo || strings.TrimSpace(card.Video) != "")
	}
	refresh := func() (bool, error) {
		cards, err := reader.readNormalVendorCode(ctx, cabinetID, vendorCode)
		if err != nil {
			return false, err
		}
		for _, card := range cards {
			reader.recent.StoreIfRecent(cabinetID, card)
			if mediaLoaded(card) {
				return true, nil
			}
		}
		return false, nil
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		loaded, err := refresh()
		if loaded {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("WB media did not become visible for vendorCode %q: %w", vendorCode, lastErr)
			}
			return fmt.Errorf("WB media did not become visible for vendorCode %q before timeout", vendorCode)
		case <-ticker.C:
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
) (CatalogObservation, error) {
	vendorCode = normalizeCatalogVendorCode(vendorCode)
	if ctx == nil || targetID <= 0 || cabinetID == "" || vendorCode == "" {
		return CatalogObservation{}, errors.New("active publication catalog query is invalid")
	}
	cards := make([]contentapi.Card, 0, 1)
	if card, ok := reader.recent.LookupVendorCode(cabinetID, vendorCode); ok {
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
) (CatalogObservation, error) {
	if ctx == nil || targetID <= 0 || cabinetID == "" {
		return CatalogObservation{}, errors.New("publication catalog query is invalid")
	}
	vendorCodes = normalizedUniqueStrings(vendorCodes)
	if len(vendorCodes) == 0 {
		return CatalogObservation{}, errors.New("publication vendor-code query is empty")
	}

	normalByID := make(map[int64]contentapi.Card)
	trashByID := make(map[int64]contentapi.TrashCard)
	for _, vendorCode := range vendorCodes {
		if card, ok := reader.recent.LookupVendorCode(cabinetID, vendorCode); ok {
			normalByID[card.NMID] = card
		} else {
			cards, err := reader.readNormalVendorCode(ctx, cabinetID, vendorCode)
			if err != nil {
				return CatalogObservation{}, err
			}
			for _, card := range cards {
				normalByID[card.NMID] = card
				reader.recent.StoreIfRecent(cabinetID, card)
			}
		}

		cards, err := reader.readTrashVendorCode(ctx, cabinetID, vendorCode)
		if err != nil {
			return CatalogObservation{}, err
		}
		for _, card := range cards {
			trashByID[card.NMID] = card
		}
	}

	return CatalogObservation{
		TargetID:   targetID,
		CabinetID:  cabinetID,
		Normal:     sortedNormalCards(normalByID),
		Trash:      sortedTrashCards(trashByID),
		ObservedAt: time.Now().UTC(),
	}, nil
}

func (reader *CatalogReader) readNormalVendorCode(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.Card, error) {
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
			return nil, fmt.Errorf("search WB normal card by vendorCode: %w", err)
		}
		for _, card := range response.Cards {
			if card.VendorCode == vendorCode {
				cardsByID[card.NMID] = card
			}
		}
		if len(response.Cards) < contentapi.MaxCardsListPageSize {
			return sortedNormalCards(cardsByID), nil
		}
		next := contentapi.CardsListCursor{
			UpdatedAt: response.Cursor.UpdatedAt,
			NMID:      response.Cursor.NMID,
			Limit:     contentapi.MaxCardsListPageSize,
		}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return nil, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, errors.New("WB normal vendor-code search pagination limit exceeded")
}

func (reader *CatalogReader) readTrashVendorCode(
	ctx context.Context,
	cabinetID CabinetID,
	vendorCode string,
) ([]contentapi.TrashCard, error) {
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
			return nil, fmt.Errorf("search WB trash card by vendorCode: %w", err)
		}
		for _, card := range response.Cards {
			if card.VendorCode == vendorCode {
				cardsByID[card.NMID] = card
			}
		}
		if len(response.Cards) < contentapi.MaxTrashCardsListPageSize {
			return sortedTrashCards(cardsByID), nil
		}
		next := contentapi.TrashCardsListCursor{
			TrashedAt: response.Cursor.TrashedAt,
			NMID:      response.Cursor.NMID,
			Limit:     contentapi.MaxTrashCardsListPageSize,
		}
		if next.TrashedAt == cursor.TrashedAt && next.NMID == cursor.NMID {
			return nil, ErrCatalogChangedDuringScan
		}
		cursor = next
	}
	return nil, errors.New("WB trash vendor-code search pagination limit exceeded")
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

func observationDraft(observation CatalogObservation) (ObservationDraft, error) {
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
