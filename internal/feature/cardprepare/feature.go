package cardprepare

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardprepare/repository/postgres"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	cardprepare_preparation "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
	cardprepare_workflow "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/workflow"

	"go.uber.org/zap"
)

type Feature struct {
	preparer       *cardprepare_preparation.Preparer
	processor      *cardprepare_workflow.Processor
	proposalReader cardprepare_workflow.ProposalReader
}

func New(
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	catalogTransport cardprepare_preparation.CatalogTransport,
	transferSource cardprepare_workflow.TransferSource,
	resultApplier cardprepare_workflow.TransferResultApplier,
	loggers ...*zap.Logger,
) *Feature {
	if postgresPool == nil {
		panic("cardprepare PostgreSQL pool is nil")
	}
	repository := cardprepare_postgres_repository.New(postgresPool)
	preparer := cardprepare_service.NewPreparer(catalogTransport, loggers...)
	return &Feature{
		preparer: preparer,
		processor: cardprepare_service.NewProcessor(
			preparer,
			transferSource,
			resultApplier,
			uow,
			loggers...,
		),
		proposalReader: repository,
	}
}

func (feature *Feature) ProposalReader() cardprepare_workflow.ProposalReader {
	return feature.proposalReader
}

func (feature *Feature) Preparer() *cardprepare_preparation.Preparer {
	return feature.preparer
}

func (feature *Feature) Processor() *cardprepare_workflow.Processor {
	return feature.processor
}
