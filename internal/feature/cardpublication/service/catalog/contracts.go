package catalog

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type CatalogTransport interface {
	CardsList(
		context.Context,
		SellerID,
		contentapi.CardsListQuery,
		contentapi.CardsListRequest,
	) (contentapi.CardsListResponse, error)

	TrashCardsList(
		context.Context,
		SellerID,
		contentapi.TrashCardsListQuery,
		contentapi.TrashCardsListRequest,
	) (contentapi.TrashCardsListResponse, error)

	CardsErrorList(
		context.Context,
		SellerID,
		contentapi.CardsErrorListQuery,
		contentapi.CardsErrorListRequest,
	) (contentapi.CardsErrorListResponse, error)

	UploadCards(
		context.Context,
		SellerID,
		domain.ClientGeneration,
		contentapi.UploadCardsRequest,
	) (contentapi.UploadCardsResponse, error)

	UploadCardsAdd(
		context.Context,
		SellerID,
		domain.ClientGeneration,
		contentapi.UploadCardsAddRequest,
	) (contentapi.UploadCardsAddResponse, error)

	SaveMediaByLinks(
		context.Context,
		SellerID,
		domain.ClientGeneration,
		contentapi.SaveMediaByLinksRequest,
	) (contentapi.SaveMediaByLinksResponse, error)
}
