package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

	"github.com/jackc/pgx/v5"
)

type frozenBatchItem struct {
	domain  cardimport_model.BatchItem
	payload []byte
}

func (r *Repository) PersistFinalized(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	finalized cardimport_contract.FinalizedSession,
) (cardimport_model.BatchHeader, error) {
	if tx == nil {
		return cardimport_model.BatchHeader{}, errors.New("persist finalized cardimport: DBTX is nil")
	}
	if err := validateFinalizedSession(finalized); err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	const insertBatch = `
		INSERT INTO wb.card_batches (
			source_session_id, author_telegram_id,
			finalized_from_revision, finalize_idempotency_key,
			items_count, groups_count
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
		ON CONFLICT (source_session_id) DO NOTHING
		RETURNING id, finalized_at;
	`
	var batchID int64
	var finalizedAt time.Time
	err := tx.QueryRow(ctx, insertBatch,
		finalized.Session.ID, finalized.User.TelegramID,
		finalized.Command.ExpectedRevision, finalized.Command.IdempotencyKey,
		len(finalized.Items), finalized.GroupsCount,
	).Scan(&batchID, &finalizedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return loadExistingFinalization(ctx, tx, finalized)
	}
	if err != nil {
		return cardimport_model.BatchHeader{}, fmt.Errorf("insert cardimport batch: %w", err)
	}
	items := make([]frozenBatchItem, len(finalized.Items))
	for index, item := range finalized.Items {
		payload, err := json.Marshal(item.Payload)
		if err != nil {
			return cardimport_model.BatchHeader{}, fmt.Errorf("marshal cardimport item: %w", err)
		}
		items[index] = frozenBatchItem{domain: item, payload: payload}
	}
	if err := copyFrozenItems(ctx, tx, cardpipeline.BatchID(batchID), items); err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	tag, err := tx.Exec(ctx,
		"UPDATE wb.card_batches SET sealed = TRUE WHERE id = $1 AND NOT sealed",
		batchID,
	)
	if err != nil {
		return cardimport_model.BatchHeader{}, fmt.Errorf("seal cardimport batch: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return cardimport_model.BatchHeader{}, fmt.Errorf("cardimport batch was not sealed: %w", core_errors.ErrConflict)
	}
	header := cardimport_model.BatchHeader{
		ID: cardpipeline.BatchID(batchID), SourceSessionID: finalized.Session.ID,
		ItemsCount: len(items), GroupsCount: finalized.GroupsCount,
		AuthorTelegramID: finalized.User.TelegramID, FinalizedAt: finalizedAt,
	}
	if err := header.Validate(); err != nil {
		return cardimport_model.BatchHeader{}, err
	}
	return header, nil
}

func validateFinalizedSession(finalized cardimport_contract.FinalizedSession) error {
	if err := finalized.Session.Validate(); err != nil {
		return err
	}
	if err := finalized.User.Validate(); err != nil {
		return err
	}
	if finalized.Session.Status != cardimport_model.SessionStatusCollecting ||
		finalized.Session.AuthorTelegramID != finalized.User.TelegramID ||
		finalized.Session.Revision != finalized.Command.ExpectedRevision ||
		finalized.Session.ID != finalized.Command.SessionID ||
		len(finalized.Files) == 0 || len(finalized.Items) == 0 {
		return fmt.Errorf("invalid finalized cardimport session: %w", core_errors.ErrConflict)
	}
	files := make(map[cardimport_model.FileID]struct{}, len(finalized.Files))
	for _, file := range finalized.Files {
		if err := file.Validate(); err != nil {
			return err
		}
		if file.SessionID != finalized.Session.ID || file.Status != cardimport_model.FileStatusValid {
			return fmt.Errorf("invalid finalized cardimport file: %w", core_errors.ErrConflict)
		}
		files[file.ID] = struct{}{}
	}
	groups := make(map[cardpipeline.SourceGroupKey]struct{})
	for index, item := range finalized.Items {
		if item.Position != index+1 {
			return fmt.Errorf("invalid finalized item position: %w", core_errors.ErrConflict)
		}
		if err := item.Validate(); err != nil {
			return err
		}
		if _, exists := files[item.SourceFileID]; !exists {
			return fmt.Errorf("finalized item source file is missing: %w", core_errors.ErrConflict)
		}
		groups[item.SourceGroupKey] = struct{}{}
	}
	if len(groups) != finalized.GroupsCount {
		return fmt.Errorf("finalized group count differs: %w", core_errors.ErrConflict)
	}
	return nil
}

func loadExistingFinalization(
	ctx context.Context, tx core_postgres_transaction.DBTX,
	finalized cardimport_contract.FinalizedSession,
) (cardimport_model.BatchHeader, error) {
	const query = `
		SELECT id, author_telegram_id, finalized_from_revision,
			finalize_idempotency_key, sealed
		FROM wb.card_batches
		WHERE source_session_id = $1
		FOR UPDATE;
	`
	var batchID int64
	var authorID int64
	var revision int64
	var key string
	var sealed bool
	if err := tx.QueryRow(ctx, query, finalized.Session.ID).Scan(
		&batchID, &authorID, &revision, &key, &sealed,
	); err != nil {
		return cardimport_model.BatchHeader{}, fmt.Errorf("cardimport session ID collision: %w", core_errors.ErrConflict)
	}
	if !sealed || authorID != finalized.User.TelegramID ||
		revision != finalized.Command.ExpectedRevision || key != finalized.Command.IdempotencyKey {
		return cardimport_model.BatchHeader{}, fmt.Errorf("cardimport finalize command differs: %w", core_errors.ErrConflict)
	}
	return loadBatch(ctx, tx, cardpipeline.BatchID(batchID))
}

func copyFrozenItems(
	ctx context.Context, tx core_postgres_transaction.DBTX,
	batchID cardpipeline.BatchID, items []frozenBatchItem,
) error {
	count, err := tx.CopyFrom(
		ctx, pgx.Identifier{"wb", "card_batch_items"},
		[]string{"batch_id", "position", "source_file_id", "source_rows", "source_group_key", "vendor_code", "payload"},
		pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			item := items[index]
			rows := make([]int32, len(item.domain.SourceRows))
			for rowIndex, row := range item.domain.SourceRows {
				rows[rowIndex] = int32(row)
			}
			return []any{batchID, item.domain.Position, item.domain.SourceFileID, rows,
				item.domain.SourceGroupKey[:], item.domain.VendorCode, string(item.payload)}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy frozen cardimport batch items: %w", err)
	}
	if count != int64(len(items)) {
		return fmt.Errorf("copied %d of %d cardimport batch items: %w", count, len(items), core_errors.ErrConflict)
	}
	return nil
}
