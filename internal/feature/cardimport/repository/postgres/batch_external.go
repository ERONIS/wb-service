package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

// CreateExternalBatch создаёт внешний батч карточек и копирует его элементы.
func (r *Repository) CreateExternalBatch(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardimport_service.ExternalBatchDraft,
) (cardimport_service.BatchHeader, error) {
	if tx == nil {
		return cardimport_service.BatchHeader{}, errors.New("create external card batch: DBTX is nil")
	}
	if err := draft.Validate(); err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	snapshotJSON, err := json.Marshal(draft.AuthorSnapshot)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("encode external batch author: %w", err)
	}
	const insert = `
		INSERT INTO wb.card_batches (
			source_session_id, source_kind, source_reference_id, purpose,
			schema_version, normalization_version, items_count, groups_count,
			checksum, author_snapshot
		)
		VALUES (NULL, $1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		ON CONFLICT (source_kind, source_reference_id) DO NOTHING
		RETURNING id, finalized_at;
	`
	var batchID int64
	var finalizedAt time.Time
	err = tx.QueryRow(
		ctx, insert, draft.SourceKind, draft.SourceReferenceID, draft.Purpose,
		cardimport_service.BatchSchemaVersion,
		cardimport_service.BatchNormalizationVersion,
		len(draft.Items), draft.GroupsCount, draft.Checksum[:], string(snapshotJSON),
	).Scan(&batchID, &finalizedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := loadBatchBySource(ctx, tx, draft.SourceKind, draft.SourceReferenceID)
		if loadErr != nil {
			return cardimport_service.BatchHeader{}, loadErr
		}
		if existing.Checksum != draft.Checksum || existing.ItemsCount != len(draft.Items) ||
			existing.GroupsCount != draft.GroupsCount {
			return cardimport_service.BatchHeader{}, fmt.Errorf("external batch replay differs from stored batch: %w", core_errors.ErrConflict)
		}
		return existing, nil
	}
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("insert external card batch: %w", err)
	}

	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "card_batch_items"},
		[]string{"batch_id", "position", "source_file_id", "source_rows", "source_group_key", "vendor_code", "payload"},
		pgx.CopyFromSlice(len(draft.Items), func(index int) ([]any, error) {
			item := draft.Items[index]
			payload, encodeErr := json.Marshal(item.Payload)
			if encodeErr != nil {
				return nil, encodeErr
			}
			rows := make([]int32, len(item.SourceRows))
			for rowIndex, row := range item.SourceRows {
				rows[rowIndex] = int32(row)
			}
			return []any{batchID, item.Position, nil, rows, item.SourceGroupKey[:], item.VendorCode, string(payload)}, nil
		}),
	)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("copy external batch items: %w", err)
	}
	if count != int64(len(draft.Items)) {
		return cardimport_service.BatchHeader{}, fmt.Errorf("copied %d of %d external batch items: %w", count, len(draft.Items), core_errors.ErrConflict)
	}
	header := cardimport_service.BatchHeader{
		ID:                   cardimport_service.BatchID(batchID),
		SourceSessionID:      cardimport_service.SessionID(draft.SourceReferenceID),
		Purpose:              draft.Purpose,
		ItemsCount:           len(draft.Items),
		GroupsCount:          draft.GroupsCount,
		SchemaVersion:        cardimport_service.BatchSchemaVersion,
		NormalizationVersion: cardimport_service.BatchNormalizationVersion,
		Checksum:             draft.Checksum,
		AuthorSnapshot:       draft.AuthorSnapshot,
		FinalizedAt:          finalizedAt,
	}
	if err := header.Validate(); err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	return header, nil
}

// loadBatchBySource загружает заголовок батча по источнику и идентификатору источника.
func loadBatchBySource(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	kind cardimport_service.BatchSourceKind,
	referenceID int64,
) (cardimport_service.BatchHeader, error) {
	query := `SELECT ` + batchColumns + ` FROM wb.card_batches AS batch
		WHERE batch.source_kind = $1 AND batch.source_reference_id = $2;`
	return scanBatchHeader(db.QueryRow(ctx, query, kind, referenceID))
}
