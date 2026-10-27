package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
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

	resumeError := service.resumeInitializing(ctx, onProgress)
	if ctx.Err() != nil {
		return errors.Join(resumeError, ctx.Err())
	}
	discoveryError := service.discoverFinalizedBatches(ctx, onProgress)
	return errors.Join(resumeError, discoveryError)
}

func (service *Service) resumeInitializing(ctx context.Context, onProgress func()) error {
	var afterID transfer_model.TransferID
	var firstError error
	failed := 0
	for {
		transfers, err := service.repository.ListInitializing(ctx, afterID, pollingPageSize)
		if err != nil {
			return errors.Join(firstError, fmt.Errorf("list initializing transfers: %w", err))
		}
		for _, transfer := range transfers {
			afterID = transfer.ID
			progress, err := service.Initialize(ctx, transfer.ID)
			if err != nil {
				if ctx.Err() != nil {
					return errors.Join(firstError, ctx.Err())
				}
				failed++
				if firstError == nil {
					firstError = fmt.Errorf("resume transfer ID='%d' initialization: %w", transfer.ID, err)
				}
				continue
			}
			if progress && onProgress != nil {
				onProgress()
			}
		}
		if len(transfers) < pollingPageSize {
			break
		}
	}
	if failed > 1 {
		return fmt.Errorf("%d transfer initializations failed in this scan; first: %w", failed, firstError)
	}
	return firstError
}

func (service *Service) discoverFinalizedBatches(ctx context.Context, onProgress func()) error {
	var after *cardpipeline.BatchCursor
	var firstError error
	failed := 0
	recordError := func(err error) {
		failed++
		if firstError == nil {
			firstError = err
		}
	}

	for {
		batches, err := service.batchReader.ListReadyBatches(ctx, after, pollingPageSize)
		if err != nil {
			return errors.Join(firstError, fmt.Errorf("list finalized transfer batches: %w", err))
		}
		for _, batch := range batches {
			after = &cardpipeline.BatchCursor{
				FinalizedAt: batch.FinalizedAt,
				BatchID:     batch.ID,
			}
			transferID, err := service.startReadyBatch(ctx, batch)
			if err != nil {
				if ctx.Err() != nil {
					return errors.Join(firstError, ctx.Err())
				}
				recordError(fmt.Errorf("start transfer for batch ID='%d': %w", batch.ID, err))
				continue
			}
			progress, err := service.Initialize(ctx, transferID)
			if err != nil {
				if ctx.Err() != nil {
					return errors.Join(firstError, ctx.Err())
				}
				recordError(fmt.Errorf("initialize transfer ID='%d': %w", transferID, err))
				continue
			}
			if progress && onProgress != nil {
				onProgress()
			}
		}
		if len(batches) < pollingPageSize {
			break
		}
	}
	if failed > 1 {
		return fmt.Errorf("%d transfer batches failed in this scan; first: %w", failed, firstError)
	}
	return firstError
}
