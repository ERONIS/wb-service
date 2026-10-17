package statistics_wb_transport

import (
	"context"
	"errors"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	wb_catalog "github.com/ERONIS/wb-service/internal/core/transport/wb/catalog"
)

type cardsSource struct {
	calls              int
	ambiguous, stalled bool
}

func (s *cardsSource) CardsList(_ context.Context, _ domain.CabinetID, _ contentapi.CardsListQuery, request contentapi.CardsListRequest) (contentapi.CardsListResponse, error) {
	s.calls++
	if s.calls == 1 {
		cards := make([]contentapi.Card, request.Settings.Cursor.Limit)
		for i := range cards {
			cards[i] = contentapi.Card{NMID: int64(i + 1), VendorCode: "similar"}
		}
		cursor := contentapi.CardsResponseCursor{NMID: 100}
		if s.stalled {
			cursor.NMID = 0
		}
		return contentapi.CardsListResponse{Cards: cards, Cursor: cursor}, nil
	}
	cards := []contentapi.Card{{NMID: 101, VendorCode: "wanted"}}
	if s.ambiguous {
		cards = append(cards, contentapi.Card{NMID: 102, VendorCode: "wanted"})
	}
	return contentapi.CardsListResponse{Cards: cards}, nil
}

func TestVerifierSearchesBeyondFirstPage(t *testing.T) {
	source := &cardsSource{}
	card, err := NewVerifier(source).FindCardByVendorCode(context.Background(), "cabinet", "wanted")
	if err != nil || card == nil || card.NMID != 101 || source.calls != 2 {
		t.Fatalf("неполная проверка: %+v, %v", card, err)
	}
}

func TestVerifierDoesNotTreatAmbiguousOrIncompleteSearchAsAbsence(t *testing.T) {
	for _, tc := range []struct {
		source cardsSource
		want   error
	}{
		{cardsSource{ambiguous: true}, core_errors.ErrConflict},
		{cardsSource{stalled: true}, wb_catalog.ErrCursorStalled},
	} {
		card, err := NewVerifier(&tc.source).FindCardByVendorCode(context.Background(), "cabinet", "wanted")
		if card != nil || !errors.Is(err, tc.want) {
			t.Fatalf("ошибка поиска потеряна: %+v, %v", card, err)
		}
	}
}
