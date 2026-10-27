package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

const (
	failureBatchMismatch   = "initialization_batch_mismatch"
	failureStorageMismatch = "initialization_storage_mismatch"
)

var (
	ErrInitializationInvalid  = core_errors.InvalidArgument("transfer initialization input is invalid")
	ErrInitializationConflict = core_errors.Conflict("transfer initialization conflicts with stored state")
)

type InitializationGroup struct {
	SourceGroupNumber int
	SourceGroupName   string
}

type InitializationItem struct {
	BatchItemID   cardpipeline.BatchItemID
	Position      int
	GroupPosition int
	VendorCode    string
	Payload       string
}

type InitializeTransferCommand struct {
	TransferID  transfer_model.TransferID
	BatchID     cardpipeline.BatchID
	ItemsCount  int
	GroupsCount int
	Groups      []InitializationGroup
	Items       []InitializationItem
}

func (command InitializeTransferCommand) Validate() error {
	if command.TransferID <= 0 || command.BatchID <= 0 ||
		command.ItemsCount <= 0 || command.GroupsCount <= 0 ||
		command.GroupsCount > command.ItemsCount ||
		len(command.Items) != command.ItemsCount ||
		len(command.Groups) != command.GroupsCount {
		return ErrInitializationInvalid
	}
	for index, group := range command.Groups {
		if group.SourceGroupNumber != index+1 || group.SourceGroupName == "" {
			return fmt.Errorf(
				"initialization group at position %d: %w",
				index+1,
				ErrInitializationInvalid,
			)
		}
	}
	for index, item := range command.Items {
		if item.BatchItemID <= 0 || item.Position != index+1 ||
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
	transferID transfer_model.TransferID,
) (bool, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return false, err
	}
	if transferID <= 0 {
		return false, core_errors.InvalidArgument("initialize transfer ID='%d'", transferID)
	}

	transfer, err := service.repository.FindByID(ctx, transferID)
	if err != nil {
		return false, fmt.Errorf("load transfer for initialization: %w", err)
	}
	if transfer.Phase != transfer_model.PhaseInitializing {
		return false, nil
	}

	command, err := service.buildInitialization(ctx, transfer)
	if err != nil {
		if core_errors.IsNotFound(err) ||
			core_errors.IsInvalidArgument(err) ||
			core_errors.IsConflict(err) {
			err = service.markInitializationFailed(
				ctx,
				transfer.ID,
				failureBatchMismatch,
			)
			return false, err
		}
		return false, err
	}

	var activated bool
	err = service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var initializeErr error
			activated, initializeErr = service.repository.Initialize(ctx, tx, command)
			return initializeErr
		},
	)
	if core_errors.IsConflict(err) {
		err = service.markInitializationFailed(
			ctx,
			transfer.ID,
			failureStorageMismatch,
		)
		return false, err
	}
	if err != nil {
		return false, fmt.Errorf("persist transfer initialization: %w", err)
	}
	return activated, nil
}

func (service *Service) buildInitialization(
	ctx context.Context,
	transfer transfer_model.Transfer,
) (InitializeTransferCommand, error) {
	ready, err := service.batchReader.GetReadyBatch(ctx, transfer.BatchID)
	if err != nil {
		return InitializeTransferCommand{}, fmt.Errorf(
			"read initialization batch: %w",
			err,
		)
	}
	batch := ready.Header
	if err := batch.Validate(); err != nil {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}
	if batch.ItemsCount != transfer.ItemsCount ||
		batch.GroupsCount != transfer.GroupsCount {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}

	if len(ready.Items) != batch.ItemsCount {
		return InitializeTransferCommand{}, ErrInitializationInvalid
	}

	groups := make([]InitializationGroup, batch.GroupsCount)
	seenGroups := make([]bool, batch.GroupsCount)
	items := make([]InitializationItem, 0, len(ready.Items))
	for _, batchItem := range ready.Items {
		if batchItem.BatchID != batch.ID || batchItem.Position != len(items)+1 ||
			batchItem.Validate() != nil {
			return InitializeTransferCommand{}, ErrInitializationInvalid
		}
		groupPosition := batchItem.SourceGroupNumber - 1
		if groupPosition < 0 || groupPosition >= len(groups) {
			return InitializeTransferCommand{}, ErrInitializationInvalid
		}
		if !seenGroups[groupPosition] {
			groups[groupPosition] = InitializationGroup{
				SourceGroupNumber: batchItem.SourceGroupNumber,
				SourceGroupName:   batchItem.Payload.Group,
			}
			seenGroups[groupPosition] = true
		} else {
			group := groups[groupPosition]
			if group.SourceGroupName != batchItem.Payload.Group {
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
			GroupPosition: groupPosition,
			VendorCode:    batchItem.Payload.Variant.VendorCode,
			Payload:       string(payload),
		})
	}
	for _, seen := range seenGroups {
		if !seen {
			return InitializeTransferCommand{}, ErrInitializationInvalid
		}
	}

	command := InitializeTransferCommand{
		TransferID:  transfer.ID,
		BatchID:     transfer.BatchID,
		ItemsCount:  transfer.ItemsCount,
		GroupsCount: transfer.GroupsCount,
		Groups:      groups,
		Items:       items,
	}
	if err := command.Validate(); err != nil {
		return InitializeTransferCommand{}, err
	}
	return command, nil
}

func (service *Service) markInitializationFailed(
	ctx context.Context,
	transferID transfer_model.TransferID,
	failureCode string,
) error {
	transfer, err := service.repository.FindByID(ctx, transferID)
	if err != nil {
		return fmt.Errorf("load transfer before initialization failure: %w", err)
	}
	if transfer.Phase != transfer_model.PhaseInitializing {
		return nil
	}

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
		if core_errors.IsConflict(err) {
			transfer, readErr := service.repository.FindByID(ctx, transferID)
			if readErr != nil {
				return fmt.Errorf("load transfer after initialization conflict: %w", readErr)
			}
			if transfer.Phase != transfer_model.PhaseInitializing {
				return nil
			}
		}
		return fmt.Errorf("mark transfer initialization failed: %w", err)
	}
	return nil
}
