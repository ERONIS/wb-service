package workflow

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

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

	ListPendingPreparationGroups(
		ctx context.Context,
		transferID transfer_service.TransferID,
		limit int,
		after transfer_service.PreparationPendingGroup,
	) ([]transfer_service.PreparationPendingGroup, error)

	LoadPreparationSource(
		ctx context.Context,
		transferID transfer_service.TransferID,
		groupTargetID int64,
	) (transfer_service.PreparationSource, error)
}

type TransferResultApplier interface {
	ApplyTx(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command transfer_service.ApplyPreparationResultCommand,
	) error
}

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
