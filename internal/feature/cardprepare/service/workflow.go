package cardprepare_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_preparation "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/workflow"

	"go.uber.org/zap"
)

func NewProcessor(
	preparer *cardprepare_preparation.Preparer,
	transferSource workflow.TransferSource,
	resultApplier workflow.TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *workflow.Processor {
	return workflow.NewProcessor(
		preparer,
		transferSource,
		resultApplier,
		uow,
		loggers...,
	)
}
