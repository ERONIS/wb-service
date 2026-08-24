package cardpublication_service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

const maxCatalogPages = 10_000

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
}

func NewCatalogReader(transport CatalogTransport) *CatalogReader {
	if transport == nil {
		panic("cardpublication catalog transport is nil")
	}
	return &CatalogReader{transport: transport}
}

func (reader *CatalogReader) Read(
	ctx context.Context,
	targetID int64,
	cabinetID CabinetID,
) (CatalogObservation, error) {
	if ctx == nil || targetID <= 0 || cabinetID == "" {
		return CatalogObservation{}, errors.New("publication catalog query is invalid")
	}
	normal, err := reader.readNormal(ctx, cabinetID)
	if err != nil {
		return CatalogObservation{}, err
	}
	trash, err := reader.readTrash(ctx, cabinetID)
	if err != nil {
		return CatalogObservation{}, err
	}
	return CatalogObservation{
		TargetID:   targetID,
		CabinetID:  cabinetID,
		Normal:     normal,
		Trash:      trash,
		ObservedAt: time.Now().UTC(),
	}, nil
}

func (reader *CatalogReader) readNormal(
	ctx context.Context,
	cabinetID CabinetID,
) ([]contentapi.Card, error) {
	cursor := contentapi.CardsListCursor{Limit: contentapi.MaxCardsListPageSize}
	cardsByID := make(map[int64]contentapi.Card)
	encodedByID := make(map[int64][]byte)
	for page := 0; page < maxCatalogPages; page++ {
		response, err := reader.transport.CardsList(
			ctx,
			cabinetID,
			contentapi.CardsListQuery{},
			contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Cursor: cursor,
			}},
		)
		if err != nil {
			return nil, fmt.Errorf("read WB normal cards page: %w", err)
		}
		for _, card := range response.Cards {
			encoded, err := json.Marshal(card)
			if err != nil {
				return nil, fmt.Errorf("encode WB normal card: %w", err)
			}
			if previous, exists := encodedByID[card.NMID]; exists {
				if !bytes.Equal(previous, encoded) {
					return nil, ErrCatalogChangedDuringScan
				}
				continue
			}
			cardsByID[card.NMID] = card
			encodedByID[card.NMID] = encoded
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
	return nil, errors.New("WB normal cards pagination limit exceeded")
}

func (reader *CatalogReader) readTrash(
	ctx context.Context,
	cabinetID CabinetID,
) ([]contentapi.TrashCard, error) {
	cursor := contentapi.TrashCardsListCursor{
		Limit: contentapi.MaxTrashCardsListPageSize,
	}
	cardsByID := make(map[int64]contentapi.TrashCard)
	encodedByID := make(map[int64][]byte)
	for page := 0; page < maxCatalogPages; page++ {
		response, err := reader.transport.TrashCardsList(
			ctx,
			cabinetID,
			contentapi.TrashCardsListQuery{},
			contentapi.TrashCardsListRequest{Settings: contentapi.TrashCardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Cursor: cursor,
			}},
		)
		if err != nil {
			return nil, fmt.Errorf("read WB trash cards page: %w", err)
		}
		for _, card := range response.Cards {
			encoded, err := json.Marshal(card)
			if err != nil {
				return nil, fmt.Errorf("encode WB trash card: %w", err)
			}
			if previous, exists := encodedByID[card.NMID]; exists {
				if !bytes.Equal(previous, encoded) {
					return nil, ErrCatalogChangedDuringScan
				}
				continue
			}
			cardsByID[card.NMID] = card
			encodedByID[card.NMID] = encoded
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
	return nil, errors.New("WB trash cards pagination limit exceeded")
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
