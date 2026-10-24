package cardimport_flow

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_batch "github.com/ERONIS/wb-service/internal/feature/cardimport/service/batch"
)

// ListReadyBatches выдаёт transfer заголовки готовых пачек.
func (s *Service) ListReadyBatches(
	ctx context.Context,
	after *cardpipeline.BatchCursor,
	limit int,
) ([]cardpipeline.ReadyBatchHeader, error) {
	batchHeaders, err := s.ListFinalizedBatches(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	ready := make([]cardpipeline.ReadyBatchHeader, len(batchHeaders))
	for index, header := range batchHeaders {
		ready[index] = readyBatchHeader(header)
	}
	return ready, nil
}

// GetReadyBatch собирает и проверяет всю пачку для transfer.
func (s *Service) GetReadyBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatch, error) {
	header, err := s.GetBatch(ctx, batchID)
	if err != nil {
		return cardpipeline.ReadyBatch{}, err
	}
	items := make([]cardimport_batch.BatchItem, 0, header.ItemsCount)
	for len(items) < header.ItemsCount {
		afterPosition := 0
		if len(items) > 0 {
			afterPosition = items[len(items)-1].Position
		}
		page, err := s.ListBatchItems(ctx, batchID, afterPosition, cardimport_batch.MaxBatchPageSize)
		if err != nil {
			return cardpipeline.ReadyBatch{}, err
		}
		if len(page) == 0 {
			return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch items are incomplete: %w", core_errors.ErrConflict)
		}
		for _, item := range page {
			if item.ID <= 0 || item.BatchID != batchID || item.Position != len(items)+1 {
				return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch item order is invalid: %w", core_errors.ErrConflict)
			}
			if err := item.Validate(); err != nil {
				return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch item is invalid: %v: %w", err, core_errors.ErrConflict)
			}
			items = append(items, item)
		}
		if len(items) > header.ItemsCount {
			return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch item count differs: %w", core_errors.ErrConflict)
		}
	}
	ready := cardpipeline.ReadyBatch{
		Header: readyBatchHeader(header),
		Items:  make([]cardpipeline.ReadyBatchItem, len(items)),
	}
	for index, item := range items {
		ready.Items[index] = cardpipeline.ReadyBatchItem{
			ID: item.ID, BatchID: item.BatchID, Position: item.Position,
			SourceGroupKey: item.SourceGroupKey, VendorCode: item.VendorCode,
			Payload: item.Payload,
		}
	}
	return ready, nil
}

// GetReadyBatchHeader возвращает transfer заголовок одной пачки.
func (s *Service) GetReadyBatchHeader(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatchHeader, error) {
	header, err := s.GetBatch(ctx, batchID)
	if err != nil {
		return cardpipeline.ReadyBatchHeader{}, err
	}
	return readyBatchHeader(header), nil
}

// readyBatchHeader убирает внутренние данные импорта из заголовка пачки.
func readyBatchHeader(header cardimport_batch.BatchHeader) cardpipeline.ReadyBatchHeader {
	return cardpipeline.ReadyBatchHeader{
		ID:         header.ID,
		ItemsCount: header.ItemsCount, GroupsCount: header.GroupsCount,
		AuthorTelegramID: header.AuthorTelegramID,
		FinalizedAt:      header.FinalizedAt,
	}
}
