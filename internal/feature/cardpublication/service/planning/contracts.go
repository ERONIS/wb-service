package planning

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type ErrorTargetSource interface {
	PublicationTargets(context.Context) ([]transfer_service.MutationTarget, error)
}

type TransferSource interface {
	ErrorTargetSource

	ListAwaitingAuthorization(
		context.Context,
		transfer_service.TransferID,
		int,
	) ([]transfer_service.Transfer, error)

	LoadPublicationPlanningSource(
		context.Context,
		transfer_service.TransferID,
	) (transfer_service.PublicationPlanningSource, error)
}

type TransferResultApplier interface {
	ApplyWithin(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_service.ApplyPublicationPlanResultCommand,
	) error
}

type ProposalReader interface {
	LoadProposals(
		context.Context,
		[]cardprepare_service.ProposalQuery,
	) ([]cardprepare_service.StoredProposal, error)
}

type Repository interface {
	InsertPlan(
		context.Context,
		core_postgres_transaction.DBTX,
		PlanDraft,
	) (PersistedPlan, error)
}
