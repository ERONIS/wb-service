package cardprepare

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardprepare/repository/postgres"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	"go.uber.org/zap"
)

type Feature struct {
	coordinator    *cardprepare_service.Coordinator
	preparer       *cardprepare_service.Preparer
	processor      *cardprepare_service.Processor
	proposalReader cardprepare_service.ProposalReader
}

func New(
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	catalogTransport cardprepare_service.CatalogTransport,
	transferSource cardprepare_service.TransferSource,
	resultApplier cardprepare_service.TransferResultApplier,
	loggers ...*zap.Logger,
) *Feature {
	if postgresPool == nil {
		panic("cardprepare PostgreSQL pool is nil")
	}
	repository := cardprepare_postgres_repository.New(postgresPool)
	preparer := cardprepare_service.NewPreparer(catalogTransport, loggers...)
	coordinator := cardprepare_service.NewCoordinator(repository, uow)
	return &Feature{
		coordinator: coordinator,
		preparer:    preparer,
		processor: cardprepare_service.NewProcessor(
			repository,
			coordinator,
			preparer,
			transferSource,
			resultApplier,
			uow,
			loggers...,
		),
		proposalReader: repository,
	}
}

func (feature *Feature) ProposalReader() cardprepare_service.ProposalReader {
	return feature.proposalReader
}

func (feature *Feature) Coordinator() *cardprepare_service.Coordinator {
	return feature.coordinator
}

func (feature *Feature) Preparer() *cardprepare_service.Preparer {
	return feature.preparer
}

func (feature *Feature) Processor() *cardprepare_service.Processor {
	return feature.processor
}
