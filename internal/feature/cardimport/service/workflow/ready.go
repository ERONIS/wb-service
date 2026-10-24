package workflow

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *Service) GetReadyBatchHeader(
	ctx context.Context,
	batchID BatchID,
) (cardpipeline.ReadyBatchHeader, error) {
	header, err := s.GetBatch(ctx, batchID)
	if err != nil {
		return cardpipeline.ReadyBatchHeader{}, err
	}
	return readyBatchHeader(header), nil
}

func (s *Service) ListReadyBatches(
	ctx context.Context,
	after *BatchCursor,
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

func (s *Service) GetReadyBatch(
	ctx context.Context,
	batchID BatchID,
) (cardpipeline.ReadyBatch, error) {
	header, err := s.GetBatch(ctx, batchID)
	if err != nil {
		return cardpipeline.ReadyBatch{}, err
	}
	items := make([]BatchItem, 0, header.ItemsCount)
	for len(items) < header.ItemsCount {
		afterPosition := 0
		if len(items) > 0 {
			afterPosition = items[len(items)-1].Position
		}
		page, err := s.ListBatchItems(ctx, batchID, afterPosition, MaxBatchPageSize)
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
			items = append(items, item)
		}
		if len(items) > header.ItemsCount {
			return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch item count differs: %w", core_errors.ErrConflict)
		}
	}
	checksum, err := BatchChecksum(header.Purpose, header.GroupsCount, items)
	if err != nil || checksum != header.Checksum {
		return cardpipeline.ReadyBatch{}, fmt.Errorf("cardimport batch checksum differs: %w", core_errors.ErrConflict)
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

func readyBatchHeader(header BatchHeader) cardpipeline.ReadyBatchHeader {
	return cardpipeline.ReadyBatchHeader{
		ID:         header.ID,
		ItemsCount: header.ItemsCount, GroupsCount: header.GroupsCount,
		Checksum:         header.Checksum,
		AuthorTelegramID: header.AuthorSnapshot.TelegramUserID,
		FinalizedAt:      header.FinalizedAt,
	}
}
