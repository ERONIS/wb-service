package transfer_service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const (
	initializationBatchPageSize = cardimport_service.MaxBatchPageSize
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
	SourceGroupKey  cardimport_service.SourceGroupKey
	SourceFileID    cardimport_service.FileID
	SourceGroupName string
}

type InitializationItem struct {
	BatchItemID   cardimport_service.BatchItemID
	Position      int
	SourceFileID  cardimport_service.FileID
	SourceRows    []int
	GroupPosition int
	VendorCode    string
	Payload       string
}

type InitializeTransferCommand struct {
	TransferID    TransferID
	BatchID       cardimport_service.BatchID
	BatchChecksum cardimport_service.Digest
	ItemsCount    int
	GroupsCount   int
	Groups        []InitializationGroup
	Items         []InitializationItem
}

func (command InitializeTransferCommand) Validate() error {
	if command.TransferID <= 0 || command.BatchID <= 0 ||
		command.BatchChecksum == (cardimport_service.Digest{}) ||
		command.ItemsCount <= 0 || command.GroupsCount <= 0 ||
		command.GroupsCount > command.ItemsCount ||
		len(command.Items) != command.ItemsCount ||
		len(command.Groups) != command.GroupsCount {
		return ErrInitializationInvalid
	}
	for index, group := range command.Groups {
		if group.SourceGroupKey == (cardimport_service.SourceGroupKey{}) ||
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
) error {
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

	transfer, err := service.repository.FindByID(ctx, transferID)
	if err != nil {
		return fmt.Errorf("load transfer for initialization: %w", err)
	}
	if transfer.Phase != PhaseInitializing {
		return nil
	}

	command, err := service.buildInitialization(ctx, transfer)
	if err != nil {
		if errors.Is(err, ErrInitializationInvalid) ||
			errors.Is(err, core_errors.ErrNotFound) ||
			errors.Is(err, core_errors.ErrInvalidArgument) ||
			errors.Is(err, core_errors.ErrConflict) {
			return service.markInitializationFailed(
				ctx,
				transfer.ID,
				failureBatchMismatch,
			)
		}
		return err
	}

	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return service.repository.Initialize(ctx, tx, command)
		},
	)
	if errors.Is(err, ErrInitializationConflict) {
		return service.markInitializationFailed(
			ctx,
			transfer.ID,
			failureStorageMismatch,
		)
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
	checksum, err := cardimport_service.BatchChecksum(
		batch.Purpose,
		batch.GroupsCount,
		batchItems,
	)
	if err != nil || checksum != batch.Checksum {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}

	groups := make([]InitializationGroup, 0, batch.GroupsCount)
	groupPositions := make(map[cardimport_service.SourceGroupKey]int, batch.GroupsCount)
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
	batch cardimport_service.BatchHeader,
) ([]cardimport_service.BatchItem, error) {
	items := make([]cardimport_service.BatchItem, 0, batch.ItemsCount)
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
