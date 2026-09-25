package workflow

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type PreparationRepository interface {
	StartPreparation(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command StartPreparationCommand,
	) error

	ListPreparationWork(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) ([]PreparationWork, error)

	SavePreparationResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command SavePreparationResultCommand,
	) (PreparationWork, error)
}

type TransferSource interface {
	ListPreparing(
		ctx context.Context,
		afterID transfer_service.TransferID,
		limit int,
	) ([]transfer_service.Transfer, error)

	BeginPreparation(
		ctx context.Context,
		transferID transfer_service.TransferID,
	) error

	LoadPreparationSource(
		ctx context.Context,
		transferID transfer_service.TransferID,
		groupTargetID int64,
	) (transfer_service.PreparationSource, error)
}

type TransferResultApplier interface {
	Apply(
		ctx context.Context,
		command transfer_service.ApplyPreparationResultCommand,
	) error
}

// ProposalReader is the only cardprepare data surface exposed to downstream
// features. Callers must present the complete immutable reference from the
// stored proposal reference.
type ProposalReader interface {
	LoadProposal(
		ctx context.Context,
		query ProposalQuery,
	) (StoredProposal, error)

	LoadProposals(
		ctx context.Context,
		queries []ProposalQuery,
	) ([]StoredProposal, error)
}
