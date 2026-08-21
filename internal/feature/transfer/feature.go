package transfer

import (
	"context"
	"fmt"
	"time"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_postgres_repository "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_config_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/config"
	transfer_polling_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/polling"
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
	service                  *transfer_service.Service
	preparationResultApplier *transfer_service.PreparationResultApplier
	pollingInterval          time.Duration
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
		repository,
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
		pollingInterval: config.PollInterval,
	}, nil
}

func (feature *Feature) Service() *transfer_service.Service {
	return feature.service
}

func (feature *Feature) PreparationResultApplier() *transfer_service.PreparationResultApplier {
	return feature.preparationResultApplier
}

func (feature *Feature) RunPolling(
	ctx context.Context,
	onError transfer_polling_transport.ErrorHandler,
	afterTransfer ...transfer_polling_transport.Processor,
) error {
	processors := make(
		[]transfer_polling_transport.Processor,
		0,
		1+len(afterTransfer),
	)
	processors = append(processors, feature.service)
	processors = append(processors, afterTransfer...)
	return transfer_polling_transport.New(
		feature.pollingInterval,
		processors...,
	).Run(ctx, onError)
}
