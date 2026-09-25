package cardprepare_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/workflow"
	"go.uber.org/zap"
)

type PreparationRepository = workflow.PreparationRepository
type TransferSource = workflow.TransferSource
type TransferResultApplier = workflow.TransferResultApplier
type ProposalReader = workflow.ProposalReader
type WorkStatus = workflow.WorkStatus
type PreparationWork = workflow.PreparationWork
type SavePreparationResultCommand = workflow.SavePreparationResultCommand
type Coordinator = workflow.Coordinator
type Processor = workflow.Processor

const (
	WorkStatusPending    = workflow.WorkStatusPending
	WorkStatusPrepared   = workflow.WorkStatusPrepared
	WorkStatusRejected   = workflow.WorkStatusRejected
	WorkStatusUnresolved = workflow.WorkStatusUnresolved
)

func NewCoordinator(
	repository PreparationRepository,
	uow core_postgres_transaction.UnitOfWork,
) *Coordinator {
	return workflow.NewCoordinator(repository, uow)
}

func NewProcessor(
	repository PreparationRepository,
	coordinator *Coordinator,
	preparer *Preparer,
	transferSource TransferSource,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *Processor {
	return workflow.NewProcessor(
		repository,
		coordinator,
		preparer,
		transferSource,
		resultApplier,
		uow,
		loggers...,
	)
}
