package cardpublication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_config_transport "github.com/ERONIS/wb-service/internal/feature/cardpublication/transport/config"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
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
	logger                   *zap.Logger
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
	loggers ...*zap.Logger,
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
	logger := core_observability.Logger(loggers...)
	repository := cardpublication_postgres_repository.New(postgresPool)
	catalogReader := cardpublication_service.NewCatalogReader(catalogTransport, logger)
	planner := cardpublication_service.NewPlanner()
	saver := cardpublication_service.NewSaver(repository, resultApplier, uow)
	errorFeed := cardpublication_service.NewErrorFeed(
		catalogTransport,
		repository,
		transferSource,
		uow,
		logger,
	)
	return &Feature{
		processor: cardpublication_service.NewProcessor(
			repository,
			transferSource,
			proposalReader,
			catalogReader,
			planner,
			saver,
			logger,
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
		logger:                   logger,
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
		feature.config.ProductConcurrency,
		feature.logger,
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
		cardpublication_service.MediaUploadMethod(feature.config.MediaUploadMethod),
		feature.config.EffectiveMediaCheckInterval(),
		feature.config.MediaCheckTimeout,
		feature.config.MediaConcurrency,
		feature.logger,
	)
}

func (feature *Feature) RunMediaPolling(
	ctx context.Context,
	onError func(error),
) error {
	if ctx == nil {
		return errors.New("run publication media polling: context is nil")
	}
	if feature.mediaDispatcher == nil {
		return errors.New("run publication media polling: dispatcher is not configured")
	}
	defer feature.mediaDispatcher.Wait()
	var workers sync.WaitGroup
	var errorMu sync.Mutex
	for _, worker := range []struct {
		interval time.Duration
		process  func(context.Context) error
	}{
		{feature.config.MediaDispatchInterval, feature.mediaDispatcher.ProcessPending},
		{feature.config.MediaDispatchInterval, feature.mediaDispatcher.ProcessMediaVisibility},
	} {
		workers.Add(1)
		go func(interval time.Duration, process func(context.Context) error) {
			defer workers.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for ctx.Err() == nil {
				if err := process(ctx); err != nil && ctx.Err() == nil && onError != nil {
					errorMu.Lock()
					onError(err)
					errorMu.Unlock()
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(worker.interval, worker.process)
	}
	workers.Wait()
	return nil
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
