package statistics_wb_transport

import (
	"context"
	"fmt"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	core_wb_config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	statistics_telegram_transport "github.com/ERONIS/wb-service/internal/feature/statistics/transport/telegram"
)

type Verifier struct {
	executors core_wb.ExecutorRegistry
}

func NewVerifier(executors core_wb.ExecutorRegistry) *Verifier {
	if executors == nil {
		panic("statistics WB executors dependency is nil")
	}
	return &Verifier{executors: executors}
}

func (verifier *Verifier) FindCardByVendorCode(
	ctx context.Context,
	cabinetID string,
	vendorCode string,
) (*statistics_telegram_transport.CardVerificationInfo, error) {
	req := contentapi.CardsListRequest{
		Settings: contentapi.CardsListSettings{
			Filter: &contentapi.CardsListFilter{
				TextSearch: vendorCode,
			},
			Cursor: contentapi.CardsListCursor{
				Limit: 10,
			},
		},
	}
	resp, err := core_wb.ExecuteForCabinet[contentapi.CardsListResponse](
		ctx,
		verifier.executors,
		core_wb_config.CabinetID(cabinetID),
		contentapi.CardsListOperation(),
		nil,
		req,
	)
	if err != nil {
		return nil, fmt.Errorf("query WB cards list for %q in cabinet %q: %w", vendorCode, cabinetID, err)
	}
	for _, card := range resp.Cards {
		if card.VendorCode == vendorCode {
			return &statistics_telegram_transport.CardVerificationInfo{
				NMID:      card.NMID,
				IMTID:     card.IMTID,
				SubjectID: card.SubjectID,
				Title:     card.Title,
				Brand:     card.Brand,
			}, nil
		}
	}
	return nil, nil
}
