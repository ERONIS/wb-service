package v1

import (
	"context"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
)

// CardsInterface описывает чтение и создание карточек товаров.
type CardsInterface interface {
	Limits(
		ctx context.Context,
	) (*contentapi.CardsLimitsResponse, error)

	List(
		ctx context.Context,
		request contentapi.CardsListRequest,
	) (*contentapi.CardsListResponse, error)

	TrashList(
		ctx context.Context,
		request contentapi.TrashCardsListRequest,
	) (*contentapi.TrashCardsListResponse, error)

	ErrorList(
		ctx context.Context,
		request contentapi.CardsErrorListRequest,
	) (*contentapi.CardsErrorListResponse, error)

	Upload(
		ctx context.Context,
		request contentapi.UploadCardsRequest,
	) (*contentapi.UploadCardsResponse, error)

	UploadAdd(
		ctx context.Context,
		request contentapi.UploadCardsAddRequest,
	) (*contentapi.UploadCardsAddResponse, error)
}

type cardsClient struct {
	apiClient client.Executor
}

func (cards *cardsClient) Limits(
	ctx context.Context,
) (*contentapi.CardsLimitsResponse, error) {
	return executeResponse[contentapi.CardsLimitsResponse](
		ctx, cards.apiClient, contentapi.CardsLimitsOperation(), nil, nil,
	)
}

func (cards *cardsClient) List(
	ctx context.Context,
	request contentapi.CardsListRequest,
) (*contentapi.CardsListResponse, error) {
	return executeResponse[contentapi.CardsListResponse](
		ctx, cards.apiClient, contentapi.CardsListOperation(), nil, request,
	)
}

func (cards *cardsClient) TrashList(
	ctx context.Context,
	request contentapi.TrashCardsListRequest,
) (*contentapi.TrashCardsListResponse, error) {
	return executeResponse[contentapi.TrashCardsListResponse](
		ctx, cards.apiClient, contentapi.TrashCardsListOperation(), nil, request,
	)
}

func (cards *cardsClient) ErrorList(
	ctx context.Context,
	request contentapi.CardsErrorListRequest,
) (*contentapi.CardsErrorListResponse, error) {
	return executeResponse[contentapi.CardsErrorListResponse](
		ctx, cards.apiClient, contentapi.CardsErrorListOperation(), nil, request,
	)
}

func (cards *cardsClient) Upload(
	ctx context.Context,
	request contentapi.UploadCardsRequest,
) (*contentapi.UploadCardsResponse, error) {
	return executeResponse[contentapi.UploadCardsResponse](
		ctx, cards.apiClient, contentapi.UploadCardsOperation(), nil, request,
	)
}

func (cards *cardsClient) UploadAdd(
	ctx context.Context,
	request contentapi.UploadCardsAddRequest,
) (*contentapi.UploadCardsAddResponse, error) {
	return executeResponse[contentapi.UploadCardsAddResponse](
		ctx, cards.apiClient, contentapi.UploadCardsAddOperation(), nil, request,
	)
}

var _ CardsInterface = (*cardsClient)(nil)
