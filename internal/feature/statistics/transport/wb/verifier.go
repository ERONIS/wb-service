package statistics_wb_transport

import (
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	wb_catalog "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1/catalog"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
)

type Verifier struct{ cards wb_catalog.CardsLister }

func NewVerifier(executors core_wb.ExecutorRegistry) *Verifier {
	if executors == nil {
		panic("statistics WB executor registry is nil")
	}
	return &Verifier{cards: cardsLister{executors: executors}}
}

type cardsLister struct{ executors core_wb.ExecutorRegistry }

func (lister cardsLister) CardsList(
	ctx context.Context,
	sellerID domain.SellerID,
	query contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsListResponse](
		ctx, lister.executors, sellerID, contentapi.CardsListOperation(), query, request,
	)
}

func (verifier *Verifier) FindCardByVendorCode(ctx context.Context, sellerID string, vendorCode string) (*statistics_service.CardVerificationInfo, error) {
	cards, _, err := wb_catalog.FindByVendorCode(ctx, verifier.cards, domain.SellerID(sellerID), vendorCode, 100)
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
