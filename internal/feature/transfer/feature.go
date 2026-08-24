package transfer

import (
	"context"
	"fmt"
	"time"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_server "github.com/ERONIS/wb-service/internal/feature/transfer/server"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_config_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/config"
	transfer_telegram_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/telegram"

	tele "gopkg.in/telebot.v3"
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
	pollingInterval             time.Duration
}

func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	batchReader cardimport_service.BatchReader,
	targetTransport transfer_service.TargetTransport,
	config Config,
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
	if err := targets.VerifyAtStartup(ctx); err != nil {
		return nil, fmt.Errorf("verify transfer targets at startup: %w", err)
	}
	service := transfer_service.New(
		repository,
		batchReader,
		targets,
		uow,
		config.CapacityPolicy(),
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
	}, nil
}

func (feature *Feature) ConfigureLiveAuthorization(
	ctx context.Context,
	bot *tele.Bot,
	menu *core_transport_telegram.Handler,
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
	transfer_telegram_transport.New(ctx, bot, service, plans).Register(menu)
	feature.liveAuthorization = service
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
	return transfer_server.NewPolling(
		feature.pollingInterval,
		processors...,
	).Run(ctx, onError)
}
