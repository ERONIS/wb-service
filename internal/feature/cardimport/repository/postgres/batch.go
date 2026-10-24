package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

const batchColumns = `
	batch.id,
	batch.source_session_id,
	batch.purpose,
	batch.items_count,
	batch.groups_count,
	batch.checksum,
	batch.author_snapshot,
	batch.finalized_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

// GetBatch загружает неизменяемый батч по его идентификатору.
func (r *Repository) GetBatch(
	ctx context.Context,
	batchID cardimport_service.BatchID,
) (cardimport_service.BatchHeader, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	return loadBatch(ctx, r.pool, batchID)
}

// ListFinalizedBatches возвращает список финализированных батчей с keyset-пагинацией.
func (r *Repository) ListFinalizedBatches(
	ctx context.Context,
	after *cardimport_service.BatchCursor,
	limit int,
) ([]cardimport_service.BatchHeader, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	query := `
		SELECT ` + batchColumns + `
		FROM wb.card_batches AS batch
		JOIN wb.card_import_sessions AS session
			ON session.finalized_batch_id = batch.id
		   AND session.id = batch.source_session_id
		   AND session.status = 'finalized'
	`
	arguments := make([]any, 0, 3)
	if after != nil {
		query += `
		WHERE (batch.finalized_at, batch.id) > ($1, $2)
		ORDER BY batch.finalized_at, batch.id
		LIMIT $3;
		`
		arguments = append(
			arguments,
			after.FinalizedAt.UTC(),
			after.BatchID,
			limit,
		)
	} else {
		query += `
		ORDER BY batch.finalized_at, batch.id
		LIMIT $1;
		`
		arguments = append(arguments, limit)
	}

	rows, err := r.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("select finalized cardimport batches: %w", err)
	}
	defer rows.Close()

	batches := make([]cardimport_service.BatchHeader, 0)
	for rows.Next() {
		header, err := scanBatchHeader(rows)
		if err != nil {
			return nil, fmt.Errorf("scan finalized cardimport batch: %w", err)
		}
		batches = append(batches, header)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate finalized cardimport batches: %w", err)
	}
	return batches, nil
}

// loadBatch выполняет SQL-запрос для чтения заголовка батча.
func loadBatch(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	batchID cardimport_service.BatchID,
) (cardimport_service.BatchHeader, error) {
	query := `
		SELECT ` + batchColumns + `
		FROM wb.card_batches AS batch
		JOIN wb.card_import_sessions AS session
			ON session.finalized_batch_id = batch.id
		   AND session.id = batch.source_session_id
		WHERE batch.id = $1
		  AND batch.source_kind = 'xlsx'
		  AND session.status = 'finalized';
	`

	header, err := scanBatchHeader(db.QueryRow(ctx, query, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"finalized cardimport batch ID='%d': %w",
			batchID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"scan finalized cardimport batch: %w",
			err,
		)
	}

	return header, nil
}

// scanBatchHeader сканирует строку в cardimport_service.BatchHeader.
func scanBatchHeader(row rowScanner) (cardimport_service.BatchHeader, error) {
	var (
		header       cardimport_service.BatchHeader
		batchID      int64
		checksum     []byte
		snapshotJSON []byte
	)
	err := row.Scan(
		&batchID,
		&header.SourceSessionID,
		&header.Purpose,
		&header.ItemsCount,
		&header.GroupsCount,
		&checksum,
		&snapshotJSON,
		&header.FinalizedAt,
	)
	if err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	if len(checksum) != len(header.Checksum) {
		return cardimport_service.BatchHeader{}, errors.New(
			"stored cardimport batch checksum has invalid length",
		)
	}
	header.ID = cardimport_service.BatchID(batchID)
	copy(header.Checksum[:], checksum)
	if err := json.Unmarshal(snapshotJSON, &header.AuthorSnapshot); err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"decode cardimport author snapshot: %w",
			err,
		)
	}
	if err := header.Validate(); err != nil {
		return cardimport_service.BatchHeader{}, err
	}

	return header, nil
}

// ListBatchItems возвращает элементы неизменяемого батча с пагинацией по позиции.
func (r *Repository) ListBatchItems(
	ctx context.Context,
	batchID cardimport_service.BatchID,
	afterPosition int,
	limit int,
) ([]cardimport_service.BatchItem, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT
			item.id,
			item.batch_id,
			item.position,
			item.source_file_id,
			item.source_rows,
			item.source_group_key,
			item.vendor_code,
			item.payload
		FROM wb.card_batch_items AS item
		JOIN wb.card_batches AS batch ON batch.id = item.batch_id
		JOIN wb.card_import_sessions AS session
			ON session.finalized_batch_id = batch.id
		   AND session.id = batch.source_session_id
		WHERE item.batch_id = $1
			AND item.position > $2
			AND batch.source_kind = 'xlsx'
			AND session.status = 'finalized'
		ORDER BY item.position
		LIMIT $3;
	`
	rows, err := r.pool.Query(ctx, query, batchID, afterPosition, limit)
	if err != nil {
		return nil, fmt.Errorf("select frozen cardimport batch items: %w", err)
	}
	defer rows.Close()

	items := make([]cardimport_service.BatchItem, 0)
	for rows.Next() {
		var (
			item        cardimport_service.BatchItem
			itemID      int64
			storedBatch int64
			sourceFile  int64
			sourceRows  []int32
			groupKey    []byte
			payload     []byte
		)
		if err := rows.Scan(
			&itemID,
			&storedBatch,
			&item.Position,
			&sourceFile,
			&sourceRows,
			&groupKey,
			&item.VendorCode,
			&payload,
		); err != nil {
			return nil, fmt.Errorf("scan frozen cardimport batch item: %w", err)
		}
		if len(groupKey) != len(item.SourceGroupKey) {
			return nil, errors.New("stored source group key has invalid length")
		}
		item.ID = cardimport_service.BatchItemID(itemID)
		item.BatchID = cardimport_service.BatchID(storedBatch)
		item.SourceFileID = cardimport_service.FileID(sourceFile)
		item.SourceRows = make([]int, len(sourceRows))
		for index, row := range sourceRows {
			item.SourceRows[index] = int(row)
		}
		copy(item.SourceGroupKey[:], groupKey)
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, fmt.Errorf("decode frozen cardimport payload: %w", err)
		}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate frozen cardimport batch items: %w", err)
	}

	return items, nil
}
