package cardpublication_service

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type TransferSource interface {
	ErrorTargetSource

	ListAwaitingAuthorization(
		ctx context.Context,
		afterID transfer_service.TransferID,
		limit int,
	) ([]transfer_service.Transfer, error)

	LoadPublicationPlanningSource(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) (transfer_service.PublicationPlanningSource, error)
}

type ErrorTargetSource interface {
	PublicationTargets(ctx context.Context) ([]transfer_service.MutationTarget, error)
}

type TransferResultApplier interface {
	ApplyWithin(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command transfer_service.ApplyPublicationPlanResultCommand,
	) error
}

type ProposalReader interface {
	LoadProposal(
		ctx context.Context,
		query cardprepare_service.ProposalQuery,
	) (cardprepare_service.StoredProposal, error)
}

// CatalogTransport performs exactly one raw WB read. Pagination, catalog
// stability checks and existing-card interpretation belong to this service.
type CatalogTransport interface {
	CardsList(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.CardsListQuery,
		request contentapi.CardsListRequest,
	) (contentapi.CardsListResponse, error)

	TrashCardsList(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.TrashCardsListQuery,
		request contentapi.TrashCardsListRequest,
	) (contentapi.TrashCardsListResponse, error)

	CardsErrorList(
		ctx context.Context,
		cabinetID CabinetID,
		query contentapi.CardsErrorListQuery,
		request contentapi.CardsErrorListRequest,
	) (contentapi.CardsErrorListResponse, error)

	UploadCards(
		ctx context.Context,
		cabinetID CabinetID,
		generation transfer_service.ClientGeneration,
		request contentapi.UploadCardsRequest,
	) (contentapi.UploadCardsResponse, error)

	UploadCardsAdd(
		ctx context.Context,
		cabinetID CabinetID,
		generation transfer_service.ClientGeneration,
		request contentapi.UploadCardsAddRequest,
	) (contentapi.UploadCardsAddResponse, error)

	SaveMediaByLinks(
		ctx context.Context,
		cabinetID CabinetID,
		generation transfer_service.ClientGeneration,
		request contentapi.SaveMediaByLinksRequest,
	) (contentapi.SaveMediaByLinksResponse, error)
}

type Repository interface {
	HasPlan(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) (bool, error)

	InsertPlan(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		draft PlanDraft,
	) (PersistedPlan, error)
}

type ErrorFeedRepository interface {
	LoadErrorCursor(ctx context.Context, cabinetID CabinetID) (ErrorCursor, error)

	SaveErrorFeed(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command SaveErrorFeedCommand,
	) (ErrorCursor, error)

	CaptureErrorBaseline(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command CaptureErrorBaselineCommand,
	) (ErrorBaseline, error)
}
