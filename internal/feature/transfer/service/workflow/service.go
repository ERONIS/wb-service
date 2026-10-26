package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type Service struct {
	repository     Repository
	batchReader    BatchReader
	targetRegistry TargetRegistry
	uow            core_postgres_transaction.UnitOfWork
	capacity       transfer_model.CapacityPolicy
	processMu      sync.Mutex
}

func New(
	repository Repository,
	batchReader BatchReader,
	targetRegistry TargetRegistry,
	uow core_postgres_transaction.UnitOfWork,
	capacity transfer_model.CapacityPolicy,
) *Service {
	if repository == nil {
		panic("transfer repository is nil")
	}
	if batchReader == nil {
		panic("transfer batch reader is nil")
	}
	if targetRegistry == nil {
		panic("transfer target registry is nil")
	}
	if uow == nil {
		panic("transfer unit of work is nil")
	}
	if err := capacity.Validate(); err != nil {
		panic(fmt.Sprintf("invalid transfer capacity policy: %v", err))
	}

	return &Service{
		repository:     repository,
		batchReader:    batchReader,
		targetRegistry: targetRegistry,
		uow:            uow,
		capacity:       capacity,
	}
}

func (service *Service) Start(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (transfer_model.TransferID, error) {
	if batchID <= 0 {
		return 0, core_errors.InvalidArgument("invalid transfer batch ID '%d'", batchID)
	}

	id, exists, err := service.findExistingTransfer(ctx, batchID)
	if err != nil || exists {
		return id, err
	}

	batch, err := service.batchReader.GetReadyBatchHeader(ctx, batchID)
	if err != nil {
		return 0, fmt.Errorf("read transfer batch: %w", err)
	}
	return service.createFromReadyBatch(ctx, batch)
}

func (service *Service) startReadyBatch(
	ctx context.Context,
	batch cardpipeline.ReadyBatchHeader,
) (transfer_model.TransferID, error) {
	id, exists, err := service.findExistingTransfer(ctx, batch.ID)
	if err != nil || exists {
		return id, err
	}
	return service.createFromReadyBatch(ctx, batch)
}

func (service *Service) findExistingTransfer(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (transfer_model.TransferID, bool, error) {
	existing, err := service.repository.FindByBatch(ctx, batchID)
	if err == nil {
		return existing.ID, true, nil
	}
	if !core_errors.IsNotFound(err) {
		return 0, false, fmt.Errorf("find existing transfer: %w", err)
	}
	return 0, false, nil
}

func (service *Service) createFromReadyBatch(
	ctx context.Context,
	batch cardpipeline.ReadyBatchHeader,
) (transfer_model.TransferID, error) {
	if err := batch.Validate(); err != nil {
		return 0, fmt.Errorf("validate transfer batch: %w", err)
	}

	targets, err := service.targetRegistry.FanoutTargets(ctx)
	if err != nil {
		return 0, fmt.Errorf("read mutation targets: %w", err)
	}
	capacity, err := service.capacity.Check(
		batch.ItemsCount,
		batch.GroupsCount,
		len(targets),
	)
	if err != nil {
		return 0, fmt.Errorf("check transfer capacity: %w", err)
	}

	var transfer transfer_model.Transfer
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			transfer, err = service.repository.Create(
				ctx,
				tx,
				CreateTransfer{
					Batch:    batch,
					Targets:  targets,
					Capacity: capacity,
				},
			)
			return err
		},
	)
	if err != nil {
		return 0, fmt.Errorf("create transfer: %w", err)
	}

	return transfer.ID, nil
}
