package cardimport_batch_repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

	"github.com/jackc/pgx/v5"
)

const batchColumns = `
	batch.id,
	batch.source_session_id,
	batch.items_count,
	batch.groups_count,
	batch.author_telegram_id,
	batch.finalized_at
`

// GetBatch загружает неизменяемый батч по его идентификатору.
func (repository *Batch) GetBatch(
	ctx context.Context,
	batchID cardpipeline.BatchID,
) (cardimport_model.BatchHeader, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	return loadBatch(ctx, repository.pool, batchID)
}

// loadBatch выполняет SQL-запрос для чтения заголовка батча.
func loadBatch(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	batchID cardpipeline.BatchID,
) (cardimport_model.BatchHeader, error) {
	query := `
		SELECT ` + batchColumns + `
		FROM wb.card_batches AS batch
		WHERE batch.id = $1
		  AND batch.sealed;
	`

	header, err := scanBatchHeader(db.QueryRow(ctx, query, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardimport_model.BatchHeader{}, fmt.Errorf(
			"finalized cardimport batch ID='%d': %w",
			batchID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return cardimport_model.BatchHeader{}, fmt.Errorf(
			"scan finalized cardimport batch: %w",
			err,
		)
	}

	return header, nil
}

// scanBatchHeader сканирует строку в cardimport_model.BatchHeader.
func scanBatchHeader(row pgx.Row) (cardimport_model.BatchHeader, error) {
	var (
		header  cardimport_model.BatchHeader
		batchID int64
	)
	err := row.Scan(
		&batchID,
		&header.SourceSessionID,
		&header.ItemsCount,
		&header.GroupsCount,
		&header.AuthorTelegramID,
		&header.FinalizedAt,
	)
	if err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	header.ID = cardpipeline.BatchID(batchID)
	if err := header.Validate(); err != nil {
		return cardimport_model.BatchHeader{}, err
	}

	return header, nil
}
