package cardpublication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	cardpublication_planning "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/planning"
	cardpublication_resolution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
	cardpublication_config_transport "github.com/ERONIS/wb-service/internal/feature/cardpublication/transport/config"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"

	"go.uber.org/zap"
)

type Feature struct {
	processor                *cardpublication_planning.Processor
	errorFeed                *cardpublication_monitoring.ErrorFeed
	livePlanSource           transfer_authorization.LivePlanSource
	productDispatcher        *cardpublication_execution.ProductDispatcher
	mediaDispatcher          *cardpublication_execution.MediaDispatcher
	manualResolver           *cardpublication_resolution.ManualResolver
	verifiedCardResolver     *cardpublication_resolution.VerifiedCardResolver
	repository               *cardpublication_postgres_repository.Repository
	transport                cardpublication_catalog.CatalogTransport
	catalogReader            *cardpublication_catalog.CatalogReader
	transferExecutionResults cardpublication_execution.PublicationExecutionResultApplier
	uow                      core_postgres_transaction.UnitOfWork
	config                   cardpublication_config_transport.Config
	logger                   *zap.Logger
}

func NewConfig() (cardpublication_config_transport.Config, error) {
	return cardpublication_config_transport.New()
}

func NewConfigMust() cardpublication_config_transport.Config {
	return cardpublication_config_transport.Must()
}

func New(
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	catalogTransport cardpublication_catalog.CatalogTransport,
	transferSource cardpublication_planning.TransferSource,
	resultApplier cardpublication_planning.TransferResultApplier,
	executionResultApplier cardpublication_execution.PublicationExecutionResultApplier,
	proposalReader cardpublication_planning.ProposalReader,
	config cardpublication_config_transport.Config,
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
	logger := core_logger.Fallback(loggers...)
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
		verifiedCardResolver: cardpublication_service.NewVerifiedCardResolver(
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
	authorization cardpublication_execution.LiveAuthorizationVerifier,
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

func (feature *Feature) MediaDispatcher() *cardpublication_execution.MediaDispatcher {
	if feature.mediaDispatcher == nil {
		panic("cardpublication media dispatcher is not configured")
	}
	return feature.mediaDispatcher
}

func (feature *Feature) ProductDispatcher() *cardpublication_execution.ProductDispatcher {
	if feature.productDispatcher == nil {
		panic("cardpublication product dispatcher is not configured")
	}
	return feature.productDispatcher
}

func (feature *Feature) ManualResolver() *cardpublication_resolution.ManualResolver {
	return feature.manualResolver
}

func (feature *Feature) VerifiedCardResolver() *cardpublication_resolution.VerifiedCardResolver {
	return feature.verifiedCardResolver
}

func (feature *Feature) ErrorFeed() *cardpublication_monitoring.ErrorFeed {
	return feature.errorFeed
}

func (feature *Feature) LivePlanSource() transfer_authorization.LivePlanSource {
	return feature.livePlanSource
}

func (feature *Feature) Processor() *cardpublication_planning.Processor {
	return feature.processor
}
