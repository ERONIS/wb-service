package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	"go.uber.org/zap"
)

const pollingPageSize = 100

func (service *Service) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	var resumeDuration, discoveryDuration, lockWait time.Duration
	defer func() {
		core_observability.LogTimingDebug(
			service.logger,
			"transfer",
			"workflow_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Duration("resume_initializing_duration", resumeDuration),
			zap.Duration("discover_batches_duration", discoveryDuration),
		)
	}()
	if ctx == nil {
		return errors.New("process pending transfers: context is nil")
	}
	lockStartedAt := time.Now()
	service.processMu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer service.processMu.Unlock()

	stepStartedAt := time.Now()
	if err := service.resumeInitializing(ctx); err != nil {
		resumeDuration = time.Since(stepStartedAt)
		return err
	}
	resumeDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	if err := service.discoverFinalizedBatches(ctx); err != nil {
		discoveryDuration = time.Since(stepStartedAt)
		return err
	}
	discoveryDuration = time.Since(stepStartedAt)
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
	batch cardpipeline.BatchHeader,
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
