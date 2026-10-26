package transfer

import (
	"context"
	"fmt"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_polling "github.com/ERONIS/wb-service/internal/feature/transfer/service/polling"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/registry"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"
	transfer_config_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/config"

	"go.uber.org/zap"
)

const (
	ModeDisabled = transfer_config_transport.ModeDisabled
	ModeDryRun   = transfer_config_transport.ModeDryRun
	ModeLive     = transfer_config_transport.ModeLive
)

type Feature struct {
	service                *workflow.Service
	repository             *transfer_postgres_repository.Repository
	uow                    core_postgres_transaction.UnitOfWork
	liveMode               bool
	authorizationTTL       time.Duration
	liveAuthorization      *authorization.LiveAuthorizationService
	automaticAuthorization *authorization.AutomaticAuthorizationProcessor
	pollingInterval        time.Duration
	logger                 *zap.Logger
}

func NewConfig() (transfer_config_transport.Config, error) {
	return transfer_config_transport.New()
}

func NewConfigMust() transfer_config_transport.Config {
	return transfer_config_transport.Must()
}

func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	cabinets registry.CabinetSource,
	config transfer_config_transport.Config,
	loggers ...*zap.Logger,
) (*Feature, error) {
	if ctx == nil {
		return nil, fmt.Errorf("initialize transfer feature: context is nil")
	}
	if postgresPool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid transfer config: %v", err))
	}

	repository := transfer_postgres_repository.New(postgresPool)
	targets := registry.NewMutationTargetRegistry(cabinets)
	service := workflow.New(
		repository,
		repository,
		targets,
		uow,
		config.CapacityPolicy(),
	)
	return &Feature{
		service:          service,
		repository:       repository,
		uow:              uow,
		liveMode:         config.Mode == ModeLive,
		authorizationTTL: config.AuthorizationTTL,
		pollingInterval:  config.PollInterval,
		logger:           core_logger.Fallback(loggers...),
	}, nil
}

func (feature *Feature) ConfigureLiveAuthorization(
	plans authorization.LivePlanSource,
) {
	if feature.liveAuthorization != nil {
		panic("transfer live authorization is already configured")
	}
	service := authorization.NewLiveAuthorizationService(
		feature.repository,
		plans,
		feature.uow,
		feature.liveMode,
		feature.authorizationTTL,
	)
	feature.liveAuthorization = service
	feature.automaticAuthorization = authorization.NewAutomaticAuthorizationProcessor(
		plans,
		service,
	)
}

func (feature *Feature) AutomaticAuthorizationProcessor() *authorization.AutomaticAuthorizationProcessor {
	if feature.automaticAuthorization == nil {
		panic("transfer automatic authorization is not configured")
	}
	return feature.automaticAuthorization
}

func (feature *Feature) LiveAuthorizationVerifier() *authorization.LiveAuthorizationService {
	if feature.liveAuthorization == nil {
		panic("transfer live authorization is not configured")
	}
	return feature.liveAuthorization
}

func (feature *Feature) PreparationProcessor(preparer preparation.CardPreparer) *preparation.Processor {
	return preparation.NewProcessor(preparer, feature.repository, feature.repository, feature.uow)
}

func (feature *Feature) PublicationSource() *transfer_postgres_repository.Repository {
	return feature.repository
}

func (feature *Feature) PublicationExecutionResults() publication.PublicationExecutionResultRepository {
	return feature.repository
}

func (feature *Feature) RunPolling(
	ctx context.Context,
	onError transfer_polling.ErrorHandler,
	afterTransfer ...transfer_polling.Processor,
) error {
	processors := make(
		[]transfer_polling.Processor,
		0,
		1+len(afterTransfer),
	)
	processors = append(processors, feature.service)
	processors = append(processors, afterTransfer...)
	return transfer_polling.NewPolling(
		feature.pollingInterval,
		processors...,
	).Run(ctx, onError, feature.logger)
}
