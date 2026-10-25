package preparation

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type CardPreparer interface {
	PrepareTarget(context.Context, core_domain.SellerID, cardprepare_model.SourceGroup) (cardprepare_model.Result, error)
}

type TransferSource interface {
	ListPreparing(context.Context, transfer_model.TransferID, int) ([]transfer_model.Transfer, error)
	BeginPreparation(context.Context, transfer_model.TransferID) error
	ListPendingPreparationGroups(context.Context, transfer_model.TransferID, int, PreparationPendingGroup) ([]PreparationPendingGroup, error)
	LoadPreparationSource(context.Context, transfer_model.TransferID, int64) (PreparationSource, error)
}

type TransferResultApplier interface {
	ApplyTx(context.Context, core_postgres_transaction.DBTX, ApplyPreparationResultCommand) error
}

type PreparationResultRepository interface {
	ApplyPreparationResult(
		context.Context,
		core_postgres_transaction.DBTX,
		ApplyPreparationResultCommand,
	) error
}
