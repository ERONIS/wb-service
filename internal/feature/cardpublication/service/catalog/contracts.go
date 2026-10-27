package catalog

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type CatalogTransport interface {
	CardsList(
		context.Context,
		domain.SellerID,
		contentapi.CardsListQuery,
		contentapi.CardsListRequest,
	) (contentapi.CardsListResponse, error)

	TrashCardsList(
		context.Context,
		domain.SellerID,
		contentapi.TrashCardsListQuery,
		contentapi.TrashCardsListRequest,
	) (contentapi.TrashCardsListResponse, error)

	CardsErrorList(
		context.Context,
		domain.SellerID,
		contentapi.CardsErrorListQuery,
		contentapi.CardsErrorListRequest,
	) (contentapi.CardsErrorListResponse, error)

	UploadCards(
		context.Context,
		domain.SellerID,
		domain.ClientGeneration,
		contentapi.UploadCardsRequest,
	) (contentapi.UploadCardsResponse, error)

	UploadCardsAdd(
		context.Context,
		domain.SellerID,
		domain.ClientGeneration,
		contentapi.UploadCardsAddRequest,
	) (contentapi.UploadCardsAddResponse, error)

	SaveMediaByLinks(
		context.Context,
		domain.SellerID,
		domain.ClientGeneration,
		contentapi.SaveMediaByLinksRequest,
	) (contentapi.SaveMediaByLinksResponse, error)
}
