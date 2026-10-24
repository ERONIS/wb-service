package workflow

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

type TransferSource interface {
	ListPreparing(
		ctx context.Context,
		afterID transfer_model.TransferID,
		limit int,
	) ([]transfer_model.Transfer, error)

	BeginPreparation(
		ctx context.Context,
		transferID transfer_model.TransferID,
	) error

	ListPendingPreparationGroups(
		ctx context.Context,
		transferID transfer_model.TransferID,
		limit int,
		after transfer_preparation.PreparationPendingGroup,
	) ([]transfer_preparation.PreparationPendingGroup, error)

	LoadPreparationSource(
		ctx context.Context,
		transferID transfer_model.TransferID,
		groupTargetID int64,
	) (transfer_preparation.PreparationSource, error)
}

type TransferResultApplier interface {
	ApplyTx(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command transfer_preparation.ApplyPreparationResultCommand,
	) error
}

type ProposalReader interface {
	LoadProposal(
		ctx context.Context,
		query cardprepare_model.ProposalQuery,
	) (cardprepare_model.StoredProposal, error)

	LoadProposals(
		ctx context.Context,
		queries []cardprepare_model.ProposalQuery,
	) ([]cardprepare_model.StoredProposal, error)
}
