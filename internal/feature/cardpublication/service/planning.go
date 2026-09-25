package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/planning"
	"go.uber.org/zap"
)

type TransferSource = planning.TransferSource
type TransferResultApplier = planning.TransferResultApplier
type ProposalReader = planning.ProposalReader
type Repository = planning.Repository
type Planner = planning.Planner
type Saver = planning.Saver
type Processor = planning.Processor

func NewPlanner() *Planner {
	return planning.NewPlanner()
}

func NewSaver(
	repository Repository,
	resultApplier TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
) *Saver {
	return planning.NewSaver(repository, resultApplier, uow)
}

func NewProcessor(
	repository Repository,
	transferSource TransferSource,
	proposalReader ProposalReader,
	catalogReader *CatalogReader,
	planner *Planner,
	saver *Saver,
	loggers ...*zap.Logger,
) *Processor {
	return planning.NewProcessor(
		repository,
		transferSource,
		proposalReader,
		catalogReader,
		planner,
		saver,
		loggers...,
	)
}
