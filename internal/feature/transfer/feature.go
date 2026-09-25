package transfer

import (
	"context"
	"fmt"
	"time"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_server "github.com/ERONIS/wb-service/internal/feature/transfer/server"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_config_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/config"
	"go.uber.org/zap"
)

type Mode = transfer_config_transport.Mode

const (
	ModeDisabled = transfer_config_transport.ModeDisabled
	ModeDryRun   = transfer_config_transport.ModeDryRun
	ModeLive     = transfer_config_transport.ModeLive
)

type Config = transfer_config_transport.Config

func NewConfig() (Config, error) {
	return transfer_config_transport.New()
}

func NewConfigMust() Config {
	return transfer_config_transport.Must()
}

type Feature struct {
	service                     *transfer_service.Service
	preparationResultApplier    *transfer_service.PreparationResultApplier
	publicationResultApplier    *transfer_service.PublicationPlanResultApplier
	publicationExecutionApplier *transfer_service.PublicationExecutionResultApplier
	repository                  *transfer_postgres_repository.Repository
	uow                         core_postgres_transaction.UnitOfWork
	liveMode                    bool
	authorizationTTL            time.Duration
	liveAuthorization           *transfer_service.LiveAuthorizationService
	automaticAuthorization      *transfer_service.AutomaticAuthorizationProcessor
	pollingInterval             time.Duration
	pollingWake                 chan struct{}
}

func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	batchReader transfer_service.BatchReader,
	targetTransport transfer_service.TargetTransport,
	config Config,
	loggers ...*zap.Logger,
) (*Feature, error) {
	if ctx == nil {
		return nil, fmt.Errorf("initialize transfer feature: context is nil")
	}
	if postgresPool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	if batchReader == nil {
		panic("transfer batch reader is nil")
	}
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid transfer config: %v", err))
	}

	repository := transfer_postgres_repository.New(postgresPool, uow)
	targets := transfer_service.NewMutationTargetRegistry(
		targetTransport,
		config.CohortName,
	)
	service := transfer_service.New(
		repository,
		batchReader,
		targets,
		uow,
		config.CapacityPolicy(),
		loggers...,
	)
	return &Feature{
		service: service,
		preparationResultApplier: transfer_service.NewPreparationResultApplier(
			repository,
			uow,
		),
		publicationResultApplier: transfer_service.NewPublicationPlanResultApplier(
			repository,
		),
		publicationExecutionApplier: transfer_service.NewPublicationExecutionResultApplier(
			repository,
		),
		repository:       repository,
		uow:              uow,
		liveMode:         config.Mode == ModeLive,
		authorizationTTL: config.AuthorizationTTL,
		pollingInterval:  config.PollInterval,
		pollingWake:      make(chan struct{}, 1),
	}, nil
}

func (feature *Feature) ConfigureLiveAuthorization(
	plans transfer_service.LivePlanSource,
) {
	if feature.liveAuthorization != nil {
		panic("transfer live authorization is already configured")
	}
	service := transfer_service.NewLiveAuthorizationService(
		feature.repository,
		plans,
		feature.uow,
		feature.liveMode,
		feature.authorizationTTL,
	)
	feature.liveAuthorization = service
	feature.automaticAuthorization = transfer_service.NewAutomaticAuthorizationProcessor(
		plans,
		service,
		feature.service.Logger(),
	)
}

func (feature *Feature) AutomaticAuthorizationProcessor() *transfer_service.AutomaticAuthorizationProcessor {
	if feature.automaticAuthorization == nil {
		panic("transfer automatic authorization is not configured")
	}
	return feature.automaticAuthorization
}

func (feature *Feature) NotifyFinalized() {
	select {
	case feature.pollingWake <- struct{}{}:
	default:
	}
}

func (feature *Feature) LiveAuthorizationVerifier() *transfer_service.LiveAuthorizationService {
	if feature.liveAuthorization == nil {
		panic("transfer live authorization is not configured")
	}
	return feature.liveAuthorization
}

func (feature *Feature) Service() *transfer_service.Service {
	return feature.service
}

func (feature *Feature) PreparationResultApplier() *transfer_service.PreparationResultApplier {
	return feature.preparationResultApplier
}

func (feature *Feature) PublicationResultApplier() *transfer_service.PublicationPlanResultApplier {
	return feature.publicationResultApplier
}

func (feature *Feature) PublicationExecutionResultApplier() *transfer_service.PublicationExecutionResultApplier {
	return feature.publicationExecutionApplier
}

func (feature *Feature) RunPolling(
	ctx context.Context,
	onError transfer_server.ErrorHandler,
	afterTransfer ...transfer_server.Processor,
) error {
	processors := make(
		[]transfer_server.Processor,
		0,
		1+len(afterTransfer),
	)
	processors = append(processors, feature.service)
	processors = append(processors, afterTransfer...)
	return transfer_server.NewPollingWithWake(
		feature.pollingInterval,
		feature.pollingWake,
		processors...,
	).Run(ctx, onError, feature.service.Logger())
}
