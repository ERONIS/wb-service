package statistics_wb_transport

import (
	"context"
	"fmt"
	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	wb_catalog "github.com/ERONIS/wb-service/internal/core/transport/wb/catalog"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	"time"
)

type Verifier struct{ cards wb_catalog.CardsLister }

func NewVerifier(cards wb_catalog.CardsLister) *Verifier {
	if cards == nil {
		panic("statistics WB cards dependency is nil")
	}
	return &Verifier{cards: cards}
}
func (verifier *Verifier) FindCardByVendorCode(ctx context.Context, cabinetID string, vendorCode string) (*statistics_service.CardVerificationInfo, error) {
	cards, _, err := wb_catalog.FindByVendorCode(ctx, verifier.cards, domain.CabinetID(cabinetID), vendorCode, 100)
	if err != nil {
		return nil, fmt.Errorf("query WB cards list: %w", err)
	}
	if len(cards) == 0 {
		return nil, nil
	}
	if len(cards) > 1 {
		return nil, core_errors.Conflict("WB vendor code matches multiple cards")
	}
	card := cards[0]
	return &statistics_service.CardVerificationInfo{
		Card: card, ObservedAt: time.Now().UTC(),
		NMID: card.NMID, IMTID: card.IMTID, SubjectID: card.SubjectID,
		Title: card.Title, Brand: card.Brand,
	}, nil
}

var _ statistics_service.CardVerifier = (*Verifier)(nil)
