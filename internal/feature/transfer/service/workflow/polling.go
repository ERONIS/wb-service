package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const pollingPageSize = 100

func (service *Service) ProcessPending(ctx context.Context) error {
	return service.ProcessPendingWithNotify(ctx, nil)
}

func (service *Service) ProcessPendingWithNotify(ctx context.Context, onProgress func()) error {
	if ctx == nil {
		return errors.New("process pending transfers: context is nil")
	}
	service.processMu.Lock()
	defer service.processMu.Unlock()

	if err := service.resumeInitializing(ctx, onProgress); err != nil {
		return err
	}
	if err := service.discoverFinalizedBatches(ctx, onProgress); err != nil {
		return err
	}
	return nil
}

func (service *Service) resumeInitializing(ctx context.Context, onProgress func()) error {
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
			progress, err := service.Initialize(ctx, transfer.ID)
			if err != nil {
				return fmt.Errorf(
					"resume transfer ID='%d' initialization: %w",
					transfer.ID,
					err,
				)
			}
			if progress && onProgress != nil {
				onProgress()
			}
			afterID = transfer.ID
		}
		if len(transfers) < pollingPageSize {
			return nil
		}
	}
}

func (service *Service) discoverFinalizedBatches(ctx context.Context, onProgress func()) error {
	for {
		batches, err := service.batchReader.ListReadyBatches(
			ctx,
			cloneBatchCursor(service.batchCursor),
			pollingPageSize,
		)
		if err != nil {
			return fmt.Errorf("list finalized transfer batches: %w", err)
		}
		for _, batch := range batches {
			if batch.Purpose != cardpipeline.PurposeTransfer {
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
			progress, err := service.Initialize(ctx, transferID)
			if err != nil {
				return fmt.Errorf(
					"initialize transfer ID='%d': %w",
					transferID,
					err,
				)
			}
			if progress && onProgress != nil {
				onProgress()
			}
			service.advanceBatchCursor(batch)
		}
		if len(batches) < pollingPageSize {
			return nil
		}
	}
}

func (service *Service) advanceBatchCursor(
	batch cardpipeline.ReadyBatchHeader,
) {
	service.batchCursor = &cardpipeline.BatchCursor{
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
	cursor *cardpipeline.BatchCursor,
) *cardpipeline.BatchCursor {
	if cursor == nil {
		return nil
	}
	clone := *cursor
	return &clone
}
