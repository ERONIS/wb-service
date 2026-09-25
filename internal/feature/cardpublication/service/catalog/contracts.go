package catalog

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

// CatalogTransport performs exactly one raw WB request. Pagination, stability
// checks, and existing-card interpretation belong to CatalogReader.
type CatalogTransport interface {
	CardsList(
		context.Context,
		CabinetID,
		contentapi.CardsListQuery,
		contentapi.CardsListRequest,
	) (contentapi.CardsListResponse, error)

	TrashCardsList(
		context.Context,
		CabinetID,
		contentapi.TrashCardsListQuery,
		contentapi.TrashCardsListRequest,
	) (contentapi.TrashCardsListResponse, error)

	CardsErrorList(
		context.Context,
		CabinetID,
		contentapi.CardsErrorListQuery,
		contentapi.CardsErrorListRequest,
	) (contentapi.CardsErrorListResponse, error)

	UploadCards(
		context.Context,
		CabinetID,
		domain.ClientGeneration,
		contentapi.UploadCardsRequest,
	) (contentapi.UploadCardsResponse, error)

	UploadCardsAdd(
		context.Context,
		CabinetID,
		domain.ClientGeneration,
		contentapi.UploadCardsAddRequest,
	) (contentapi.UploadCardsAddResponse, error)

	SaveMediaByLinks(
		context.Context,
		CabinetID,
		domain.ClientGeneration,
		contentapi.SaveMediaByLinksRequest,
	) (contentapi.SaveMediaByLinksResponse, error)
}
