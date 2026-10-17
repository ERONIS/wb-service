package cardprepare_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/workflow"
	"go.uber.org/zap"
)

type TransferSource = workflow.TransferSource
type TransferResultApplier = workflow.TransferResultApplier
type ProposalReader = workflow.ProposalReader
type Processor = workflow.Processor

func NewProcessor(
	preparer *Preparer,
	transferSource TransferSource,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *Processor {
	return workflow.NewProcessor(
		preparer,
		transferSource,
		resultApplier,
		uow,
		loggers...,
	)
}
