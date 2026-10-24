package cardpublication_wb_transport

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
)

type CatalogTransport struct {
	executors core_wb.PinnedExecutorRegistry
}

func NewCatalogTransport(executors core_wb.PinnedExecutorRegistry) *CatalogTransport {
	if executors == nil {
		panic("cardpublication WB executor registry is nil")
	}
	return &CatalogTransport{executors: executors}
}

func (transport *CatalogTransport) CardsList(
	ctx context.Context,
	sellerID domain.SellerID,
	query contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsListResponse](
		ctx, transport.executors, sellerID, contentapi.CardsListOperation(), query, request,
	)
}

func (transport *CatalogTransport) TrashCardsList(
	ctx context.Context,
	sellerID domain.SellerID,
	query contentapi.TrashCardsListQuery,
	request contentapi.TrashCardsListRequest,
) (contentapi.TrashCardsListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.TrashCardsListResponse](
		ctx, transport.executors, sellerID, contentapi.TrashCardsListOperation(), query, request,
	)
}

func (transport *CatalogTransport) CardsErrorList(
	ctx context.Context,
	sellerID domain.SellerID,
	query contentapi.CardsErrorListQuery,
	request contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return core_wb.ExecuteForCabinet[contentapi.CardsErrorListResponse](
		ctx, transport.executors, sellerID, contentapi.CardsErrorListOperation(), query, request,
	)
}

func (transport *CatalogTransport) UploadCards(
	ctx context.Context,
	sellerID domain.SellerID,
	generation domain.ClientGeneration,
	request contentapi.UploadCardsRequest,
) (contentapi.UploadCardsResponse, error) {
	return core_wb.ExecutePinnedForCabinet[contentapi.UploadCardsResponse](
		ctx, transport.executors, sellerID, generation, contentapi.UploadCardsOperation(), nil, request,
	)
}

func (transport *CatalogTransport) UploadCardsAdd(
	ctx context.Context,
	sellerID domain.SellerID,
	generation domain.ClientGeneration,
	request contentapi.UploadCardsAddRequest,
) (contentapi.UploadCardsAddResponse, error) {
	return core_wb.ExecutePinnedForCabinet[contentapi.UploadCardsAddResponse](
		ctx, transport.executors, sellerID, generation, contentapi.UploadCardsAddOperation(), nil, request,
	)
}

func (transport *CatalogTransport) SaveMediaByLinks(
	ctx context.Context,
	sellerID domain.SellerID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return core_wb.ExecutePinnedForCabinet[contentapi.SaveMediaByLinksResponse](
		ctx, transport.executors, sellerID, generation, contentapi.SaveMediaByLinksOperation(), nil, request,
	)
}

var _ cardpublication_catalog.CatalogTransport = (*CatalogTransport)(nil)
