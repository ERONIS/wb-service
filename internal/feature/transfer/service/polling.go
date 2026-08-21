package transfer_service

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const pollingPageSize = 100

func (service *Service) ProcessPending(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process pending transfers: context is nil")
	}
	service.processMu.Lock()
	defer service.processMu.Unlock()

	if err := service.resumeInitializing(ctx); err != nil {
		return err
	}
	if err := service.discoverFinalizedBatches(ctx); err != nil {
		return err
	}
	return nil
}

func (service *Service) resumeInitializing(ctx context.Context) error {
	var afterID TransferID
	for {
		transfers, err := service.repository.ListInitializing(
			ctx,
			afterID,
			pollingPageSize,
		)
		if err != nil {
			return fmt.Errorf("list initializing transfers: %w", err)
		}
		for _, transfer := range transfers {
			if err := service.Initialize(ctx, transfer.ID); err != nil {
				return fmt.Errorf(
					"resume transfer ID='%d' initialization: %w",
					transfer.ID,
					err,
				)
			}
			afterID = transfer.ID
		}
		if len(transfers) < pollingPageSize {
			return nil
		}
	}
}

func (service *Service) discoverFinalizedBatches(ctx context.Context) error {
	for {
		batches, err := service.batchReader.ListFinalizedBatches(
			ctx,
			cloneBatchCursor(service.batchCursor),
			pollingPageSize,
		)
		if err != nil {
			return fmt.Errorf("list finalized transfer batches: %w", err)
		}
		for _, batch := range batches {
			if batch.Purpose != cardimport_service.PurposeTransfer {
				service.advanceBatchCursor(batch)
				continue
			}
			transferID, err := service.Start(ctx, batch.ID)
			if err != nil {
				if isDeterministicStartRejection(err) {
					service.advanceBatchCursor(batch)
				}
				return fmt.Errorf(
					"start transfer for batch ID='%d': %w",
					batch.ID,
					err,
				)
			}
			if err := service.Initialize(ctx, transferID); err != nil {
				return fmt.Errorf(
					"initialize transfer ID='%d': %w",
					transferID,
					err,
				)
			}
			service.advanceBatchCursor(batch)
		}
		if len(batches) < pollingPageSize {
			return nil
		}
	}
}

func (service *Service) advanceBatchCursor(
	batch cardimport_service.BatchHeader,
) {
	service.batchCursor = &cardimport_service.BatchCursor{
		FinalizedAt: batch.FinalizedAt,
		BatchID:     batch.ID,
	}
}

func isDeterministicStartRejection(err error) bool {
	return errors.Is(err, ErrCapacityExceeded) ||
		errors.Is(err, core_errors.ErrConflict) ||
		errors.Is(err, core_errors.ErrInvalidArgument)
}

func cloneBatchCursor(
	cursor *cardimport_service.BatchCursor,
) *cardimport_service.BatchCursor {
	if cursor == nil {
		return nil
	}
	clone := *cursor
	return &clone
}
