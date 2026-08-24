package cardpublication_wb_transport

import (
	"context"
	"fmt"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type ExecutorRegistry interface {
	ExecutorForCabinet(id wbconfig.CabinetID) (client.Executor, error)
	PinnedExecutor(
		id wbconfig.CabinetID,
		generation core_wb.ClientGeneration,
	) (client.Executor, error)
}

type CatalogTransport struct {
	executors ExecutorRegistry
}

func NewCatalogTransport(executors ExecutorRegistry) *CatalogTransport {
	if executors == nil {
		panic("cardpublication WB executor registry is nil")
	}
	return &CatalogTransport{executors: executors}
}

func (transport *CatalogTransport) CardsList(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	query contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return executeRaw[contentapi.CardsListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.CardsListOperation(),
		query,
		request,
	)
}

func (transport *CatalogTransport) TrashCardsList(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	query contentapi.TrashCardsListQuery,
	request contentapi.TrashCardsListRequest,
) (contentapi.TrashCardsListResponse, error) {
	return executeRaw[contentapi.TrashCardsListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.TrashCardsListOperation(),
		query,
		request,
	)
}

func (transport *CatalogTransport) CardsErrorList(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	query contentapi.CardsErrorListQuery,
	request contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return executeRaw[contentapi.CardsErrorListResponse](
		ctx,
		transport.executors,
		cabinetID,
		contentapi.CardsErrorListOperation(),
		query,
		request,
	)
}

func (transport *CatalogTransport) UploadCards(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	generation transfer_service.ClientGeneration,
	request contentapi.UploadCardsRequest,
) (contentapi.UploadCardsResponse, error) {
	return executePinnedRaw[contentapi.UploadCardsResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.UploadCardsOperation(),
		request,
	)
}

func (transport *CatalogTransport) UploadCardsAdd(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	generation transfer_service.ClientGeneration,
	request contentapi.UploadCardsAddRequest,
) (contentapi.UploadCardsAddResponse, error) {
	return executePinnedRaw[contentapi.UploadCardsAddResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.UploadCardsAddOperation(),
		request,
	)
}

func (transport *CatalogTransport) SaveMediaByLinks(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
	generation transfer_service.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return executePinnedRaw[contentapi.SaveMediaByLinksResponse](
		ctx,
		transport.executors,
		cabinetID,
		generation,
		contentapi.SaveMediaByLinksOperation(),
		request,
	)
}

func executeRaw[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	cabinetID cardpublication_service.CabinetID,
	operation policy.Operation,
	query any,
	body any,
) (Response, error) {
	executor, err := executors.ExecutorForCabinet(wbconfig.CabinetID(cabinetID))
	if err != nil {
		var response Response
		return response, fmt.Errorf("get WB cabinet executor: %w", err)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, query, body)
}

func executePinnedRaw[Response any](
	ctx context.Context,
	executors ExecutorRegistry,
	cabinetID cardpublication_service.CabinetID,
	generation transfer_service.ClientGeneration,
	operation policy.Operation,
	body any,
) (Response, error) {
	var coreGeneration core_wb.ClientGeneration
	copy(coreGeneration[:], generation[:])
	executor, err := executors.PinnedExecutor(
		wbconfig.CabinetID(cabinetID),
		coreGeneration,
	)
	if err != nil {
		var response Response
		return response, fmt.Errorf("get pinned WB cabinet executor: %w", err)
	}
	return client.ExecuteResponse[Response](ctx, executor, operation, nil, body)
}

var _ cardpublication_service.CatalogTransport = (*CatalogTransport)(nil)
