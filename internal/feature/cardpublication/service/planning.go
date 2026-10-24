package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/planning"

	"go.uber.org/zap"
)

func NewPlanner() *planning.Planner {
	return planning.NewPlanner()
}

func NewSaver(
	repository planning.Repository,
	resultApplier planning.TransferResultApplier,
	uow core_postgres_transaction.UnitOfWork,
) *planning.Saver {
	return planning.NewSaver(repository, resultApplier, uow)
}

func NewProcessor(
	repository planning.Repository,
	transferSource planning.TransferSource,
	proposalReader planning.ProposalReader,
	catalogReader *cardpublication_catalog.CatalogReader,
	planner *planning.Planner,
	saver *planning.Saver,
	loggers ...*zap.Logger,
) *planning.Processor {
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
