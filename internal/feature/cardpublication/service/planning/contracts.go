package planning

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type TransferSource interface {
	ListReadyForPublicationPlanning(
		context.Context,
		transfer_model.TransferID,
		int,
	) ([]transfer_model.Transfer, error)

	LoadPublicationPlanningSource(
		context.Context,
		transfer_model.TransferID,
	) (transfer_publication.PublicationPlanningSource, error)
}

type TransferResultRepository interface {
	ApplyPublicationPlanResult(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_publication.ApplyPublicationPlanResultCommand,
	) error
}

type Repository interface {
	InsertPlan(
		context.Context,
		core_postgres_transaction.DBTX,
		cardpublication_model.PlanDraft,
	) (cardpublication_model.PersistedPlan, error)
}
