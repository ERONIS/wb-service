package cardpublication

import (
	"fmt"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_config_transport "github.com/ERONIS/wb-service/internal/feature/cardpublication/transport/config"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type Feature struct {
	processor                *cardpublication_service.Processor
	errorFeed                *cardpublication_service.ErrorFeed
	livePlanSource           transfer_service.LivePlanSource
	productDispatcher        *cardpublication_service.ProductDispatcher
	mediaDispatcher          *cardpublication_service.MediaDispatcher
	manualResolver           *cardpublication_service.ManualResolver
	repository               *cardpublication_postgres_repository.Repository
	transport                cardpublication_service.CatalogTransport
	catalogReader            *cardpublication_service.CatalogReader
	transferExecutionResults cardpublication_service.PublicationExecutionResultApplier
	uow                      core_postgres_transaction.UnitOfWork
	config                   Config
}

type Config = cardpublication_config_transport.Config

func NewConfig() (Config, error) {
	return cardpublication_config_transport.New()
}

func NewConfigMust() Config {
	return cardpublication_config_transport.Must()
}

func New(
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	catalogTransport cardpublication_service.CatalogTransport,
	transferSource cardpublication_service.TransferSource,
	resultApplier cardpublication_service.TransferResultApplier,
	executionResultApplier cardpublication_service.PublicationExecutionResultApplier,
	proposalReader cardpublication_service.ProposalReader,
	config Config,
) *Feature {
	if postgresPool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	if executionResultApplier == nil {
		panic("cardpublication execution result applier is nil")
	}
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid cardpublication config: %v", err))
	}
	repository := cardpublication_postgres_repository.New(postgresPool)
	catalogReader := cardpublication_service.NewCatalogReader(catalogTransport)
	planner := cardpublication_service.NewPlanner()
	saver := cardpublication_service.NewSaver(repository, resultApplier, uow)
	errorFeed := cardpublication_service.NewErrorFeed(
		catalogTransport,
		repository,
		transferSource,
		uow,
	)
	return &Feature{
		processor: cardpublication_service.NewProcessor(
			repository,
			transferSource,
			proposalReader,
			catalogReader,
			planner,
			saver,
		),
		errorFeed:      errorFeed,
		livePlanSource: repository,
		manualResolver: cardpublication_service.NewManualResolver(
			repository,
			executionResultApplier,
			uow,
		),
		repository:               repository,
		transport:                catalogTransport,
		catalogReader:            catalogReader,
		transferExecutionResults: executionResultApplier,
		uow:                      uow,
		config:                   config,
	}
}

func (feature *Feature) ConfigureProductDispatch(
	authorization cardpublication_service.LiveAuthorizationVerifier,
) {
	if feature.productDispatcher != nil {
		panic("cardpublication product dispatcher is already configured")
	}
	feature.productDispatcher = cardpublication_service.NewProductDispatcher(
		feature.repository,
		feature.transport,
		feature.catalogReader,
		feature.errorFeed,
		authorization,
		feature.transferExecutionResults,
		feature.uow,
		feature.config.ReconciliationDelay,
		feature.config.ReconciliationTimeout,
	)
	feature.mediaDispatcher = cardpublication_service.NewMediaDispatcher(
		feature.repository,
		feature.transport,
		feature.catalogReader,
		feature.errorFeed,
		authorization,
		feature.transferExecutionResults,
		feature.uow,
		feature.config.MediaAutoDispatch,
	)
}

func (feature *Feature) MediaDispatcher() *cardpublication_service.MediaDispatcher {
	if feature.mediaDispatcher == nil {
		panic("cardpublication media dispatcher is not configured")
	}
	return feature.mediaDispatcher
}

func (feature *Feature) ProductDispatcher() *cardpublication_service.ProductDispatcher {
	if feature.productDispatcher == nil {
		panic("cardpublication product dispatcher is not configured")
	}
	return feature.productDispatcher
}

func (feature *Feature) ManualResolver() *cardpublication_service.ManualResolver {
	return feature.manualResolver
}

func (feature *Feature) ErrorFeed() *cardpublication_service.ErrorFeed {
	return feature.errorFeed
}

func (feature *Feature) LivePlanSource() transfer_service.LivePlanSource {
	return feature.livePlanSource
}

func (feature *Feature) Processor() *cardpublication_service.Processor {
	return feature.processor
}
