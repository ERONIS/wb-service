package transfer_service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

type Service struct {
	repository     Repository
	batchReader    BatchReader
	targetRegistry TargetRegistry
	uow            core_postgres_transaction.UnitOfWork
	capacity       CapacityPolicy
	processMu      sync.Mutex
	batchCursor    *cardimport_service.BatchCursor
}

func New(
	repository Repository,
	batchReader BatchReader,
	targetRegistry TargetRegistry,
	uow core_postgres_transaction.UnitOfWork,
	capacity CapacityPolicy,
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
	batchID cardimport_service.BatchID,
) (TransferID, error) {
	if batchID <= 0 {
		return 0, fmt.Errorf(
			"invalid transfer batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	}

	existing, err := service.repository.FindByBatch(ctx, batchID)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, core_errors.ErrNotFound) {
		return 0, fmt.Errorf("find existing transfer: %w", err)
	}

	batch, err := service.batchReader.GetBatch(ctx, batchID)
	if err != nil {
		return 0, fmt.Errorf("read transfer batch: %w", err)
	}
	if err := validateStartBatch(batch); err != nil {
		return 0, err
	}

	snapshot, err := service.targetRegistry.MutationSnapshot(ctx)
	if err != nil {
		return 0, fmt.Errorf("read mutation target snapshot: %w", err)
	}
	capacity, err := service.capacity.Check(
		batch.ItemsCount,
		batch.GroupsCount,
		len(snapshot.Targets),
	)
	if err != nil {
		return 0, fmt.Errorf("check transfer capacity: %w", err)
	}
	root := TargetSetRoot(snapshot)

	var transfer Transfer
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			transfer, _, err = service.repository.Create(
				ctx,
				tx,
				CreateTransfer{
					Batch:         batch,
					Snapshot:      snapshot,
					TargetSetRoot: root,
					Capacity:      capacity,
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

func validateStartBatch(batch cardimport_service.BatchHeader) error {
	if err := batch.Validate(); err != nil {
		return fmt.Errorf("validate transfer batch: %w", err)
	}
	if batch.Purpose != cardimport_service.PurposeTransfer {
		return fmt.Errorf(
			"cardimport batch purpose '%s' cannot start transfer: %w",
			batch.Purpose,
			core_errors.ErrConflict,
		)
	}
	if batch.SchemaVersion != cardimport_service.BatchSchemaVersion ||
		batch.NormalizationVersion != cardimport_service.BatchNormalizationVersion {
		return fmt.Errorf(
			"cardimport batch contract is unsupported: %w",
			core_errors.ErrConflict,
		)
	}
	if batch.Checksum == (cardimport_service.Digest{}) {
		return fmt.Errorf(
			"cardimport batch checksum is empty: %w",
			core_errors.ErrConflict,
		)
	}
	return nil
}
