package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

	"github.com/jackc/pgx/v5"
)

const readyBatchColumns = `
	batch.id,
	batch.source_session_id,
	batch.items_count,
	batch.groups_count,
	batch.author_telegram_id,
	batch.finalized_at
`

type readyBatchRowScanner interface {
	Scan(dest ...any) error
}

// ListReadyBatches читает завершённые пачки напрямую из PostgreSQL.
func (repository *Repository) ListReadyBatches(
	ctx context.Context,
	after *cardpipeline.BatchCursor,
	limit int,
) ([]cardpipeline.ReadyBatchHeader, error) {
	if after != nil {
		if err := after.Validate(); err != nil {
			return nil, err
		}
	}
	if limit <= 0 || limit > cardimport_model.MaxBatchPageSize {
		return nil, core_errors.InvalidArgument("invalid ready batch page limit '%d'", limit)
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `SELECT ` + readyBatchColumns + ` FROM wb.card_batches AS batch WHERE batch.sealed`
	arguments := make([]any, 0, 3)
	if after != nil {
		query += ` AND (batch.finalized_at, batch.id) > ($1, $2)
			ORDER BY batch.finalized_at, batch.id LIMIT $3`
		arguments = append(arguments, after.FinalizedAt.UTC(), after.BatchID, limit)
	} else {
		query += ` ORDER BY batch.finalized_at, batch.id LIMIT $1`
		arguments = append(arguments, limit)
	}
	rows, err := repository.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list ready transfer batches: %w", err)
	}
	defer rows.Close()

	headers := make([]cardpipeline.ReadyBatchHeader, 0)
	for rows.Next() {
		header, err := scanReadyBatchHeader(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ready transfer batch: %w", err)
		}
		headers = append(headers, header)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ready transfer batches: %w", err)
	}
	return headers, nil
}

// GetReadyBatchHeader читает заголовок завершённой пачки по ID.
func (repository *Repository) GetReadyBatchHeader(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatchHeader, error) {
	if batchID <= 0 {
		return cardpipeline.ReadyBatchHeader{}, core_errors.InvalidArgument("invalid ready batch ID '%d'", batchID)
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `SELECT ` + readyBatchColumns + ` FROM wb.card_batches AS batch
		WHERE batch.id = $1 AND batch.sealed`
	header, err := scanReadyBatchHeader(repository.pool.QueryRow(ctx, query, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpipeline.ReadyBatchHeader{}, fmt.Errorf("ready transfer batch ID='%d': %w", batchID, core_errors.ErrNotFound)
	}
	if err != nil {
		return cardpipeline.ReadyBatchHeader{}, fmt.Errorf("scan ready transfer batch: %w", err)
	}
	return header, nil
}

// GetReadyBatch читает и проверяет все элементы завершённой пачки.
func (repository *Repository) GetReadyBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatch, error) {
	header, err := repository.GetReadyBatchHeader(ctx, batchID)
	if err != nil {
		return cardpipeline.ReadyBatch{}, err
	}
	items := make([]cardpipeline.ReadyBatchItem, 0, header.ItemsCount)
	for len(items) < header.ItemsCount {
		afterPosition := 0
		if len(items) > 0 {
			afterPosition = items[len(items)-1].Position
		}
		page, err := repository.listReadyBatchItems(ctx, batchID, afterPosition)
		if err != nil {
			return cardpipeline.ReadyBatch{}, err
		}
		if len(page) == 0 {
			return cardpipeline.ReadyBatch{}, fmt.Errorf("ready transfer batch items are incomplete: %w", core_errors.ErrConflict)
		}
		for _, item := range page {
			if item.BatchID != batchID || item.Position != len(items)+1 {
				return cardpipeline.ReadyBatch{}, fmt.Errorf("ready transfer batch item order is invalid: %w", core_errors.ErrConflict)
			}
			items = append(items, item)
		}
		if len(items) > header.ItemsCount {
			return cardpipeline.ReadyBatch{}, fmt.Errorf("ready transfer batch item count differs: %w", core_errors.ErrConflict)
		}
	}
	return cardpipeline.ReadyBatch{Header: header, Items: items}, nil
}

// listReadyBatchItems читает одну страницу элементов пачки.
func (repository *Repository) listReadyBatchItems(
	ctx context.Context,
	batchID cardpipeline.BatchID,
	afterPosition int,
) ([]cardpipeline.ReadyBatchItem, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT item.id, item.batch_id, item.position, item.source_file_id,
			item.source_rows, item.source_group_key, item.vendor_code, item.payload
		FROM wb.card_batch_items AS item
		JOIN wb.card_batches AS batch ON batch.id = item.batch_id
		WHERE item.batch_id = $1 AND item.position > $2 AND batch.sealed
		ORDER BY item.position LIMIT $3
	`
	rows, err := repository.pool.Query(ctx, query, batchID, afterPosition, cardimport_model.MaxBatchPageSize)
	if err != nil {
		return nil, fmt.Errorf("list ready transfer batch items: %w", err)
	}
	defer rows.Close()
	items := make([]cardpipeline.ReadyBatchItem, 0)
	for rows.Next() {
		item, err := scanReadyBatchItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ready transfer batch item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ready transfer batch items: %w", err)
	}
	return items, nil
}

// scanReadyBatchHeader проверяет заголовок перед передачей в workflow.
func scanReadyBatchHeader(row readyBatchRowScanner) (cardpipeline.ReadyBatchHeader, error) {
	var header cardimport_model.BatchHeader
	var batchID, sourceSessionID int64
	if err := row.Scan(
		&batchID, &sourceSessionID, &header.ItemsCount,
		&header.GroupsCount, &header.AuthorTelegramID, &header.FinalizedAt,
	); err != nil {
		return cardpipeline.ReadyBatchHeader{}, err
	}
	header.ID = cardpipeline.BatchID(batchID)
	header.SourceSessionID = cardimport_model.SessionID(sourceSessionID)
	if err := header.Validate(); err != nil {
		return cardpipeline.ReadyBatchHeader{}, fmt.Errorf("stored ready batch header is invalid: %v: %w", err, core_errors.ErrConflict)
	}
	return cardpipeline.ReadyBatchHeader{
		ID: header.ID, ItemsCount: header.ItemsCount, GroupsCount: header.GroupsCount,
		AuthorTelegramID: header.AuthorTelegramID, FinalizedAt: header.FinalizedAt,
	}, nil
}

// scanReadyBatchItem проверяет данные и происхождение карточки перед передачей в workflow.
func scanReadyBatchItem(row readyBatchRowScanner) (cardpipeline.ReadyBatchItem, error) {
	var item cardimport_model.BatchItem
	var itemID, batchID, sourceFileID int64
	var sourceRows []int32
	var sourceGroupKey, payload []byte
	if err := row.Scan(
		&itemID, &batchID, &item.Position, &sourceFileID,
		&sourceRows, &sourceGroupKey, &item.VendorCode, &payload,
	); err != nil {
		return cardpipeline.ReadyBatchItem{}, err
	}
	if len(sourceGroupKey) != len(item.SourceGroupKey) {
		return cardpipeline.ReadyBatchItem{}, fmt.Errorf("stored source group key length is invalid: %w", core_errors.ErrConflict)
	}
	item.ID = cardpipeline.BatchItemID(itemID)
	item.BatchID = cardpipeline.BatchID(batchID)
	item.SourceFileID = cardimport_model.FileID(sourceFileID)
	item.SourceRows = make([]int, len(sourceRows))
	for index, sourceRow := range sourceRows {
		item.SourceRows[index] = int(sourceRow)
	}
	copy(item.SourceGroupKey[:], sourceGroupKey)
	if err := json.Unmarshal(payload, &item.Payload); err != nil {
		return cardpipeline.ReadyBatchItem{}, fmt.Errorf("decode stored ready batch item: %v: %w", err, core_errors.ErrConflict)
	}
	if item.ID <= 0 || item.BatchID <= 0 {
		return cardpipeline.ReadyBatchItem{}, fmt.Errorf("stored ready batch item identity is invalid: %w", core_errors.ErrConflict)
	}
	if err := item.Validate(); err != nil {
		return cardpipeline.ReadyBatchItem{}, fmt.Errorf("stored ready batch item is invalid: %v: %w", err, core_errors.ErrConflict)
	}
	return cardpipeline.ReadyBatchItem{
		ID: item.ID, BatchID: item.BatchID, Position: item.Position,
		SourceGroupKey: item.SourceGroupKey, VendorCode: item.VendorCode, Payload: item.Payload,
	}, nil
}
