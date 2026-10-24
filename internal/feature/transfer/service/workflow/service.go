package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"go.uber.org/zap"
)

type Service struct {
	repository     Repository
	batchReader    BatchReader
	targetRegistry TargetRegistry
	uow            core_postgres_transaction.UnitOfWork
	capacity       transfer_model.CapacityPolicy
	logger         *zap.Logger
	processMu      sync.Mutex
	batchCursor    *cardpipeline.BatchCursor
}

func New(
	repository Repository,
	batchReader BatchReader,
	targetRegistry TargetRegistry,
	uow core_postgres_transaction.UnitOfWork,
	capacity transfer_model.CapacityPolicy,
	loggers ...*zap.Logger,
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
		logger:         core_logger.Fallback(loggers...),
	}
}

func (service *Service) Logger() *zap.Logger { return service.logger }

func (service *Service) Start(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (transfer_model.TransferID, error) {
	return service.start(ctx, batchID, nil)
}

func (service *Service) StartToCabinet(
	ctx context.Context,
	batchID cardpipeline.BatchID,
	sellerID core_domain.SellerID,
) (transfer_model.TransferID, error) {
	if strings.TrimSpace(string(sellerID)) != string(sellerID) || sellerID == "" {
		return 0, core_errors.InvalidArgument("invalid transfer seller ID %q", sellerID)
	}
	return service.start(ctx, batchID, []core_domain.SellerID{sellerID})
}

func (service *Service) start(
	ctx context.Context,
	batchID cardpipeline.BatchID,
	targetSellerIDs []core_domain.SellerID,
) (transfer_model.TransferID, error) {
	if batchID <= 0 {
		return 0, core_errors.InvalidArgument("invalid transfer batch ID '%d'", batchID)
	}

	existing, err := service.repository.FindByBatch(ctx, batchID)
	if err == nil {
		if len(targetSellerIDs) > 0 {
			if err := service.verifyStoredTargets(ctx, existing.ID, targetSellerIDs); err != nil {
				return 0, err
			}
		}
		return existing.ID, nil
	}
	if !core_errors.IsNotFound(err) {
		return 0, fmt.Errorf("find existing transfer: %w", err)
	}

	batch, err := service.batchReader.GetReadyBatchHeader(ctx, batchID)
	if err != nil {
		return 0, fmt.Errorf("read transfer batch: %w", err)
	}
	if err := validateStartBatch(batch); err != nil {
		return 0, err
	}

	var snapshot transfer_model.MutationTargetSnapshot
	if len(targetSellerIDs) == 0 {
		snapshot, err = service.targetRegistry.FanoutMutationSnapshot(ctx)
	} else {
		snapshot, err = service.targetRegistry.MutationSnapshotForOwner(
			ctx,
			batch.AuthorTelegramID,
			targetSellerIDs,
		)
	}
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

	var transfer transfer_model.Transfer
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			var created bool
			transfer, created, err = service.repository.Create(
				ctx,
				tx,
				CreateTransfer{
					Batch:    batch,
					Snapshot: snapshot,
					Capacity: capacity,
				},
			)
			if err != nil || created || len(targetSellerIDs) == 0 {
				return err
			}
			stored, err := service.repository.ListTargetSellerIDsWithin(ctx, tx, transfer.ID)
			if err != nil {
				return fmt.Errorf("read stored transfer targets: %w", err)
			}
			return verifyTargetSellerIDs(stored, targetSellerIDs)
		},
	)
	if err != nil {
		return 0, fmt.Errorf("create transfer: %w", err)
	}

	return transfer.ID, nil
}

func (service *Service) verifyStoredTargets(
	ctx context.Context,
	transferID transfer_model.TransferID,
	expected []core_domain.SellerID,
) error {
	stored, err := service.repository.ListTargetSellerIDs(ctx, transferID)
	if err != nil {
		return fmt.Errorf("read stored transfer targets: %w", err)
	}
	return verifyTargetSellerIDs(stored, expected)
}

func verifyTargetSellerIDs(stored, expected []core_domain.SellerID) error {
	if len(stored) != len(expected) {
		return core_errors.Conflict("transfer target set differs from requested target")
	}
	wanted := make(map[core_domain.SellerID]struct{}, len(expected))
	for _, sellerID := range expected {
		wanted[sellerID] = struct{}{}
	}
	for _, sellerID := range stored {
		if _, exists := wanted[sellerID]; !exists {
			return core_errors.Conflict("transfer target set differs from requested target")
		}
	}
	return nil
}

func validateStartBatch(batch cardpipeline.ReadyBatchHeader) error {
	if err := batch.Validate(); err != nil {
		return fmt.Errorf("validate transfer batch: %w", err)
	}
	return nil
}
