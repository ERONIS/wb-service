package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

var (
	ErrPageLimit     = errors.New("WB catalog pagination limit exceeded")
	ErrCursorStalled = fmt.Errorf("WB catalog cursor did not advance: %w", core_errors.ErrConflict)
)

type CardsLister interface {
	CardsList(context.Context, domain.CabinetID, contentapi.CardsListQuery, contentapi.CardsListRequest) (contentapi.CardsListResponse, error)
}

type TrashCardsLister interface {
	TrashCardsList(context.Context, domain.CabinetID, contentapi.TrashCardsListQuery, contentapi.TrashCardsListRequest) (contentapi.TrashCardsListResponse, error)
}

// WalkCards обходит страницы, исключая повторные nmID; false из visit завершает обход досрочно.
// Достижение ограничения страниц не означает отсутствия карточки и возвращает ErrPageLimit.
func WalkCards(ctx context.Context, source CardsLister, cabinetID domain.CabinetID, query contentapi.CardsListQuery, request contentapi.CardsListRequest, maxPages int, visit func(contentapi.Card) bool) (int, error) {
	if request.Settings.Cursor.Limit == 0 {
		request.Settings.Cursor.Limit = contentapi.MaxCardsListPageSize
	}
	limit := request.Settings.Cursor.Limit
	if source == nil || limit < 1 || limit > contentapi.MaxCardsListPageSize || visit == nil {
		return 0, core_errors.InvalidArgument("invalid WB cards scan")
	}
	return walkPages(ctx, request.Settings.Cursor, limit, maxPages,
		func(cursor contentapi.CardsListCursor) ([]contentapi.Card, contentapi.CardsListCursor, error) {
			request.Settings.Cursor = cursor
			response, err := source.CardsList(ctx, cabinetID, query, request)
			return response.Cards, contentapi.CardsListCursor{UpdatedAt: response.Cursor.UpdatedAt, NMID: response.Cursor.NMID, Limit: limit}, err
		}, func(card contentapi.Card) int64 { return card.NMID }, visit)
}

// WalkTrashCards применяет те же гарантии обхода к корзине WB.
func WalkTrashCards(ctx context.Context, source TrashCardsLister, cabinetID domain.CabinetID, query contentapi.TrashCardsListQuery, request contentapi.TrashCardsListRequest, maxPages int, visit func(contentapi.TrashCard) bool) (int, error) {
	if request.Settings.Cursor.Limit == 0 {
		request.Settings.Cursor.Limit = contentapi.MaxTrashCardsListPageSize
	}
	limit := request.Settings.Cursor.Limit
	if source == nil || limit < 1 || limit > contentapi.MaxTrashCardsListPageSize || visit == nil {
		return 0, core_errors.InvalidArgument("invalid WB trash scan")
	}
	return walkPages(ctx, request.Settings.Cursor, limit, maxPages,
		func(cursor contentapi.TrashCardsListCursor) ([]contentapi.TrashCard, contentapi.TrashCardsListCursor, error) {
			request.Settings.Cursor = cursor
			response, err := source.TrashCardsList(ctx, cabinetID, query, request)
			return response.Cards, contentapi.TrashCardsListCursor{TrashedAt: response.Cursor.TrashedAt, NMID: response.Cursor.NMID, Limit: limit}, err
		}, func(card contentapi.TrashCard) int64 { return card.NMID }, visit)
}

func walkPages[Card any, Cursor comparable](ctx context.Context, cursor Cursor, limit, maxPages int, read func(Cursor) ([]Card, Cursor, error), identity func(Card) int64, visit func(Card) bool) (int, error) {
	if ctx == nil || maxPages <= 0 {
		return 0, core_errors.InvalidArgument("invalid WB pagination context or limit")
	}
	seenCards := make(map[int64]struct{})
	seenCursors := map[Cursor]struct{}{cursor: {}}
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return page - 1, err
		}
		cards, next, err := read(cursor)
		if err != nil {
			return page, err
		}
		for _, card := range cards {
			id := identity(card)
			if id <= 0 {
				return page, errors.New("WB catalog contains invalid nmID")
			}
			if _, exists := seenCards[id]; exists {
				continue
			}
			seenCards[id] = struct{}{}
			if !visit(card) {
				return page, nil
			}
		}
		if len(cards) < limit {
			return page, nil
		}
		if _, exists := seenCursors[next]; exists {
			return page, ErrCursorStalled
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
	return maxPages, ErrPageLimit
}

// FindByVendorCode возвращает все точные совпадения после полного обхода поисковой выдачи.
func FindByVendorCode(ctx context.Context, source CardsLister, cabinetID domain.CabinetID, vendorCode string, maxPages int) ([]contentapi.Card, int, error) {
	if vendorCode == "" {
		return nil, 0, core_errors.InvalidArgument("vendor code is empty")
	}
	cards := make([]contentapi.Card, 0, 1)
	pages, err := WalkCards(ctx, source, cabinetID, contentapi.CardsListQuery{}, contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
		Sort: contentapi.CardsSort{Ascending: true}, Filter: &contentapi.CardsListFilter{TextSearch: vendorCode},
	}}, maxPages, func(card contentapi.Card) bool {
		if card.VendorCode == vendorCode {
			cards = append(cards, card)
		}
		return true
	})
	if err != nil {
		return nil, pages, err
	}
	return cards, pages, nil
}
