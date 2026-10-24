package planning

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type ErrorTargetSource interface {
	PublicationTargets(context.Context) ([]transfer_model.MutationTarget, error)
}

type TransferSource interface {
	ErrorTargetSource

	ListAwaitingAuthorization(
		context.Context,
		transfer_model.TransferID,
		int,
	) ([]transfer_model.Transfer, error)

	LoadPublicationPlanningSource(
		context.Context,
		transfer_model.TransferID,
	) (transfer_publication.PublicationPlanningSource, error)
}

type TransferResultApplier interface {
	ApplyWithin(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_publication.ApplyPublicationPlanResultCommand,
	) error
}

type ProposalReader interface {
	LoadProposals(
		context.Context,
		[]cardprepare_model.ProposalQuery,
	) ([]cardprepare_model.StoredProposal, error)
}

type Repository interface {
	InsertPlan(
		context.Context,
		core_postgres_transaction.DBTX,
		cardpublication_model.PlanDraft,
	) (cardpublication_model.PersistedPlan, error)
}
