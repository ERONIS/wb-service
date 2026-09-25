package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"go.uber.org/zap"
)

type Service struct {
	repository     Repository
	batchReader    BatchReader
	targetRegistry TargetRegistry
	uow            core_postgres_transaction.UnitOfWork
	capacity       CapacityPolicy
	logger         *zap.Logger
	processMu      sync.Mutex
	batchCursor    *cardpipeline.BatchCursor
}

func New(
	repository Repository,
	batchReader BatchReader,
	targetRegistry TargetRegistry,
	uow core_postgres_transaction.UnitOfWork,
	capacity CapacityPolicy,
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
		logger:         core_observability.Logger(loggers...),
	}
}

func (service *Service) Logger() *zap.Logger { return service.logger }

func (service *Service) Start(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (TransferID, error) {
	return service.start(ctx, batchID, nil)
}

// StartToCabinet starts the existing transfer pipeline for one exact target.
func (service *Service) StartToCabinet(
	ctx context.Context,
	batchID cardpipeline.BatchID,
	cabinetID CabinetID,
) (TransferID, error) {
	if strings.TrimSpace(string(cabinetID)) != string(cabinetID) || cabinetID == "" {
		return 0, fmt.Errorf("invalid transfer cabinet ID %q: %w", cabinetID, core_errors.ErrInvalidArgument)
	}
	return service.start(ctx, batchID, []CabinetID{cabinetID})
}

func (service *Service) start(
	ctx context.Context,
	batchID cardpipeline.BatchID,
	targetCabinetIDs []CabinetID,
) (transferID TransferID, err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{BatchID: int64(batchID)})
	var (
		findExistingDuration time.Duration
		verifyStoredDuration time.Duration
		readBatchDuration    time.Duration
		readTargetsDuration  time.Duration
		capacityDuration     time.Duration
		persistDuration      time.Duration
		targetsCount         int
		itemsCount           int
		groupsCount          int
		reused               bool
	)
	defer func() {
		core_observability.LogTiming(
			service.logger,
			"transfer",
			"start",
			startedAt,
			err,
			zap.Int64("batch_id", int64(batchID)),
			zap.Int64("transfer_id", int64(transferID)),
			zap.Int("requested_targets_count", len(targetCabinetIDs)),
			zap.Int("targets_count", targetsCount),
			zap.Int("items_count", itemsCount),
			zap.Int("groups_count", groupsCount),
			zap.Bool("reused", reused),
			zap.Duration("find_existing_duration", findExistingDuration),
			zap.Duration("verify_stored_targets_duration", verifyStoredDuration),
			zap.Duration("read_batch_duration", readBatchDuration),
			zap.Duration("read_targets_duration", readTargetsDuration),
			zap.Duration("capacity_check_duration", capacityDuration),
			zap.Duration("persist_duration", persistDuration),
		)
	}()
	if batchID <= 0 {
		return 0, fmt.Errorf(
			"invalid transfer batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	}

	stepStartedAt := time.Now()
	existing, err := service.repository.FindByBatch(ctx, batchID)
	findExistingDuration = time.Since(stepStartedAt)
	if err == nil {
		itemsCount = existing.ItemsCount
		groupsCount = existing.GroupsCount
		targetsCount = existing.TargetsCount
		if len(targetCabinetIDs) > 0 {
			stepStartedAt = time.Now()
			if err := service.verifyStoredTargets(ctx, existing.ID, targetCabinetIDs); err != nil {
				verifyStoredDuration = time.Since(stepStartedAt)
				return 0, err
			}
			verifyStoredDuration = time.Since(stepStartedAt)
		}
		transferID = existing.ID
		reused = true
		return transferID, nil
	}
	if !errors.Is(err, core_errors.ErrNotFound) {
		return 0, fmt.Errorf("find existing transfer: %w", err)
	}

	stepStartedAt = time.Now()
	batch, err := service.batchReader.GetBatch(ctx, batchID)
	readBatchDuration = time.Since(stepStartedAt)
	if err != nil {
		return 0, fmt.Errorf("read transfer batch: %w", err)
	}
	if err := validateStartBatch(batch); err != nil {
		return 0, err
	}
	itemsCount = batch.ItemsCount
	groupsCount = batch.GroupsCount
	core_observability.LogStarted(service.logger, "transfer", "start",
		zap.Int64("batch_id", int64(batchID)), zap.Int("items_count", itemsCount),
		zap.Duration("batch_age", time.Since(batch.FinalizedAt)))

	var snapshot MutationTargetSnapshot
	if len(targetCabinetIDs) == 0 {
		stepStartedAt = time.Now()
		snapshot, err = service.targetRegistry.FanoutMutationSnapshot(ctx)
	} else {
		stepStartedAt = time.Now()
		snapshot, err = service.targetRegistry.MutationSnapshotForOwner(
			ctx,
			batch.AuthorSnapshot.TelegramUserID,
			targetCabinetIDs,
		)
	}
	readTargetsDuration = time.Since(stepStartedAt)
	if err != nil {
		return 0, fmt.Errorf("read mutation target snapshot: %w", err)
	}
	targetsCount = len(snapshot.Targets)
	stepStartedAt = time.Now()
	capacity, err := service.capacity.Check(
		batch.ItemsCount,
		batch.GroupsCount,
		len(snapshot.Targets),
	)
	capacityDuration = time.Since(stepStartedAt)
	if err != nil {
		return 0, fmt.Errorf("check transfer capacity: %w", err)
	}
	root := TargetSetRoot(snapshot)

	var transfer Transfer
	stepStartedAt = time.Now()
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			var created bool
			transfer, created, err = service.repository.Create(
				ctx,
				tx,
				CreateTransfer{
					Batch:         batch,
					Snapshot:      snapshot,
					TargetSetRoot: root,
					Capacity:      capacity,
				},
			)
			if err != nil || created || len(targetCabinetIDs) == 0 {
				return err
			}
			return service.verifyStoredTargets(ctx, transfer.ID, targetCabinetIDs)
		},
	)
	persistDuration = time.Since(stepStartedAt)
	if err != nil {
		return 0, fmt.Errorf("create transfer: %w", err)
	}

	transferID = transfer.ID
	return transferID, nil
}

func (service *Service) verifyStoredTargets(
	ctx context.Context,
	transferID TransferID,
	expected []CabinetID,
) error {
	stored, err := service.repository.ListTargetCabinetIDs(ctx, transferID)
	if err != nil {
		return fmt.Errorf("read stored transfer targets: %w", err)
	}
	if len(stored) != len(expected) {
		return fmt.Errorf("transfer target set differs from requested target: %w", core_errors.ErrConflict)
	}
	wanted := make(map[CabinetID]struct{}, len(expected))
	for _, cabinetID := range expected {
		wanted[cabinetID] = struct{}{}
	}
	for _, cabinetID := range stored {
		if _, exists := wanted[cabinetID]; !exists {
			return fmt.Errorf("transfer target set differs from requested target: %w", core_errors.ErrConflict)
		}
	}
	return nil
}

func validateStartBatch(batch cardpipeline.BatchHeader) error {
	if err := batch.Validate(); err != nil {
		return fmt.Errorf("validate transfer batch: %w", err)
	}
	if batch.Purpose != cardpipeline.PurposeTransfer {
		return fmt.Errorf(
			"cardimport batch purpose '%s' cannot start transfer: %w",
			batch.Purpose,
			core_errors.ErrConflict,
		)
	}
	if batch.SchemaVersion != cardpipeline.BatchSchemaVersion ||
		batch.NormalizationVersion != cardpipeline.BatchNormalizationVersion {
		return fmt.Errorf(
			"cardimport batch contract is unsupported: %w",
			core_errors.ErrConflict,
		)
	}
	if batch.Checksum == (cardpipeline.Digest{}) {
		return fmt.Errorf(
			"cardimport batch checksum is empty: %w",
			core_errors.ErrConflict,
		)
	}
	return nil
}
