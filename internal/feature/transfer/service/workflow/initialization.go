package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"go.uber.org/zap"
)

const (
	initializationBatchPageSize = cardpipeline.MaxBatchPageSize
	failureBatchMismatch        = "initialization_batch_mismatch"
	failureStorageMismatch      = "initialization_storage_mismatch"
)

var (
	ErrInitializationInvalid = fmt.Errorf(
		"transfer initialization input is invalid: %w",
		core_errors.ErrInvalidArgument,
	)
	ErrInitializationConflict = fmt.Errorf(
		"transfer initialization conflicts with stored state: %w",
		core_errors.ErrConflict,
	)
)

type InitializationGroup struct {
	SourceGroupKey  cardpipeline.SourceGroupKey
	SourceFileID    cardpipeline.FileID
	SourceGroupName string
}

type InitializationItem struct {
	BatchItemID   cardpipeline.BatchItemID
	Position      int
	SourceFileID  cardpipeline.FileID
	SourceRows    []int
	GroupPosition int
	VendorCode    string
	Payload       string
}

type InitializeTransferCommand struct {
	TransferID    TransferID
	BatchID       cardpipeline.BatchID
	BatchChecksum cardpipeline.Digest
	ItemsCount    int
	GroupsCount   int
	Groups        []InitializationGroup
	Items         []InitializationItem
}

func (command InitializeTransferCommand) Validate() error {
	if command.TransferID <= 0 || command.BatchID <= 0 ||
		command.BatchChecksum == (cardpipeline.Digest{}) ||
		command.ItemsCount <= 0 || command.GroupsCount <= 0 ||
		command.GroupsCount > command.ItemsCount ||
		len(command.Items) != command.ItemsCount ||
		len(command.Groups) != command.GroupsCount {
		return ErrInitializationInvalid
	}
	for index, group := range command.Groups {
		if group.SourceGroupKey == (cardpipeline.SourceGroupKey{}) ||
			group.SourceFileID <= 0 {
			return fmt.Errorf(
				"initialization group at position %d: %w",
				index+1,
				ErrInitializationInvalid,
			)
		}
	}
	for index, item := range command.Items {
		if item.BatchItemID <= 0 || item.Position != index+1 ||
			item.SourceFileID <= 0 || len(item.SourceRows) == 0 ||
			item.GroupPosition < 0 || item.GroupPosition >= len(command.Groups) ||
			item.VendorCode == "" || item.Payload == "" {
			return fmt.Errorf(
				"initialization item at position %d: %w",
				index+1,
				ErrInitializationInvalid,
			)
		}
	}
	return nil
}

func (service *Service) Initialize(
	ctx context.Context,
	transferID TransferID,
) (err error) {
	startedAt := time.Now()
	var (
		loadTransferDuration time.Duration
		buildCommandDuration time.Duration
		persistDuration      time.Duration
		markFailureDuration  time.Duration
		failureCode          string
		batchID              cardpipeline.BatchID
		itemsCount           int
		groupsCount          int
		skipped              bool
	)
	defer func() {
		core_observability.LogTiming(
			service.logger,
			"transfer",
			"initialize",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(transferID)),
			zap.Int64("batch_id", int64(batchID)),
			zap.Int("items_count", itemsCount),
			zap.Int("groups_count", groupsCount),
			zap.Bool("skipped", skipped),
			zap.String("failure_code", failureCode),
			zap.Duration("load_transfer_duration", loadTransferDuration),
			zap.Duration("build_command_duration", buildCommandDuration),
			zap.Duration("persist_duration", persistDuration),
			zap.Duration("mark_failure_duration", markFailureDuration),
		)
	}()
	if ctx == nil {
		return errors.New("initialize transfer: context is nil")
	}
	if transferID <= 0 {
		return fmt.Errorf(
			"initialize transfer ID='%d': %w",
			transferID,
			core_errors.ErrInvalidArgument,
		)
	}

	stepStartedAt := time.Now()
	transfer, err := service.repository.FindByID(ctx, transferID)
	loadTransferDuration = time.Since(stepStartedAt)
	if err != nil {
		return fmt.Errorf("load transfer for initialization: %w", err)
	}
	batchID = transfer.BatchID
	itemsCount = transfer.ItemsCount
	groupsCount = transfer.GroupsCount
	if transfer.Phase != PhaseInitializing {
		skipped = true
		return nil
	}
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(transferID), BatchID: int64(batchID),
	})
	core_observability.LogStarted(service.logger, "transfer", "initialize",
		zap.Int64("transfer_id", int64(transferID)), zap.Int64("batch_id", int64(batchID)))

	stepStartedAt = time.Now()
	command, err := service.buildInitialization(ctx, transfer)
	buildCommandDuration = time.Since(stepStartedAt)
	if err != nil {
		if errors.Is(err, ErrInitializationInvalid) ||
			errors.Is(err, core_errors.ErrNotFound) ||
			errors.Is(err, core_errors.ErrInvalidArgument) ||
			errors.Is(err, core_errors.ErrConflict) {
			failureCode = failureBatchMismatch
			stepStartedAt = time.Now()
			err = service.markInitializationFailed(
				ctx,
				transfer.ID,
				failureBatchMismatch,
			)
			markFailureDuration = time.Since(stepStartedAt)
			return err
		}
		return err
	}

	stepStartedAt = time.Now()
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return service.repository.Initialize(ctx, tx, command)
		},
	)
	persistDuration = time.Since(stepStartedAt)
	if errors.Is(err, ErrInitializationConflict) {
		failureCode = failureStorageMismatch
		stepStartedAt = time.Now()
		err = service.markInitializationFailed(
			ctx,
			transfer.ID,
			failureStorageMismatch,
		)
		markFailureDuration = time.Since(stepStartedAt)
		return err
	}
	if err != nil {
		return fmt.Errorf("persist transfer initialization: %w", err)
	}
	return nil
}

func (service *Service) buildInitialization(
	ctx context.Context,
	transfer Transfer,
) (InitializeTransferCommand, error) {
	batch, err := service.batchReader.GetBatch(ctx, transfer.BatchID)
	if err != nil {
		return InitializeTransferCommand{}, fmt.Errorf(
			"read initialization batch: %w",
			err,
		)
	}
	if batch.ItemsCount != transfer.ItemsCount ||
		batch.GroupsCount != transfer.GroupsCount ||
		batch.SchemaVersion != transfer.BatchSchemaVersion ||
		batch.NormalizationVersion != transfer.BatchNormalizationVersion ||
		batch.Checksum != transfer.BatchChecksum {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}

	batchItems, err := service.readAllBatchItems(ctx, batch)
	if err != nil {
		return InitializeTransferCommand{}, err
	}
	checksum, err := cardpipeline.BatchChecksum(
		batch.Purpose,
		batch.GroupsCount,
		batchItems,
	)
	if err != nil || checksum != batch.Checksum {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}

	groups := make([]InitializationGroup, 0, batch.GroupsCount)
	groupPositions := make(map[cardpipeline.SourceGroupKey]int, batch.GroupsCount)
	items := make([]InitializationItem, 0, len(batchItems))
	for _, batchItem := range batchItems {
		groupPosition, exists := groupPositions[batchItem.SourceGroupKey]
		if !exists {
			groupPosition = len(groups)
			groupPositions[batchItem.SourceGroupKey] = groupPosition
			groups = append(groups, InitializationGroup{
				SourceGroupKey:  batchItem.SourceGroupKey,
				SourceFileID:    batchItem.SourceFileID,
				SourceGroupName: batchItem.Payload.Group,
			})
		} else {
			group := groups[groupPosition]
			if group.SourceFileID != batchItem.SourceFileID ||
				group.SourceGroupName != batchItem.Payload.Group {
				return InitializeTransferCommand{}, ErrInitializationInvalid
			}
		}

		payload, err := json.Marshal(batchItem.Payload)
		if err != nil {
			return InitializeTransferCommand{}, fmt.Errorf(
				"encode batch item at position %d: %w",
				batchItem.Position,
				err,
			)
		}
		items = append(items, InitializationItem{
			BatchItemID:   batchItem.ID,
			Position:      batchItem.Position,
			SourceFileID:  batchItem.SourceFileID,
			SourceRows:    append([]int(nil), batchItem.SourceRows...),
			GroupPosition: groupPosition,
			VendorCode:    batchItem.VendorCode,
			Payload:       string(payload),
		})
	}

	command := InitializeTransferCommand{
		TransferID:    transfer.ID,
		BatchID:       transfer.BatchID,
		BatchChecksum: transfer.BatchChecksum,
		ItemsCount:    transfer.ItemsCount,
		GroupsCount:   transfer.GroupsCount,
		Groups:        groups,
		Items:         items,
	}
	if err := command.Validate(); err != nil {
		return InitializeTransferCommand{}, err
	}
	return command, nil
}

func (service *Service) readAllBatchItems(
	ctx context.Context,
	batch cardpipeline.BatchHeader,
) ([]cardpipeline.BatchItem, error) {
	items := make([]cardpipeline.BatchItem, 0, batch.ItemsCount)
	afterPosition := 0
	for {
		page, err := service.batchReader.ListBatchItems(
			ctx,
			batch.ID,
			afterPosition,
			initializationBatchPageSize,
		)
		if err != nil {
			return nil, fmt.Errorf("read initialization batch items: %w", err)
		}
		for _, item := range page {
			if item.ID <= 0 || item.BatchID != batch.ID ||
				item.Position != len(items)+1 || item.Validate() != nil {
				return nil, ErrInitializationInvalid
			}
			items = append(items, item)
		}
		if len(page) < initializationBatchPageSize {
			break
		}
		afterPosition = items[len(items)-1].Position
	}
	if len(items) != batch.ItemsCount {
		return nil, ErrInitializationInvalid
	}
	return items, nil
}

func (service *Service) markInitializationFailed(
	ctx context.Context,
	transferID TransferID,
	failureCode string,
) error {
	if err := service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return service.repository.MarkInitializationFailed(
				ctx,
				tx,
				transferID,
				failureCode,
			)
		},
	); err != nil {
		return fmt.Errorf("mark transfer initialization failed: %w", err)
	}
	return nil
}
