package cardimport_batch_repository

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

type batchSourceGroup struct {
	fileID cardimport_model.FileID
	name   string
}

func (repository *Batch) PersistFinalized(
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
		len(finalized.SourceFiles) == 0 || len(finalized.Items) == 0 {
		return fmt.Errorf("invalid finalized cardimport session: %w", core_errors.ErrConflict)
	}
	files := make(map[cardimport_model.FileID]struct{}, len(finalized.SourceFiles))
	for _, sourceFile := range finalized.SourceFiles {
		if sourceFile.ID <= 0 || sourceFile.SessionID != finalized.Session.ID {
			return fmt.Errorf("invalid finalized cardimport file identity: %w", core_errors.ErrConflict)
		}
		if _, exists := files[sourceFile.ID]; exists {
			return fmt.Errorf("duplicate finalized cardimport file ID: %w", core_errors.ErrConflict)
		}
		files[sourceFile.ID] = struct{}{}
	}
	usedFiles := make(map[cardimport_model.FileID]struct{}, len(files))
	groupNumbers := make(map[batchSourceGroup]int)
	groupsByNumber := make(map[int]batchSourceGroup)
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
		usedFiles[item.SourceFileID] = struct{}{}
		group := batchSourceGroup{fileID: item.SourceFileID, name: item.Payload.Group}
		if number, exists := groupNumbers[group]; exists && number != item.SourceGroupNumber {
			return fmt.Errorf("finalized source group has multiple numbers: %w", core_errors.ErrConflict)
		}
		if previous, exists := groupsByNumber[item.SourceGroupNumber]; exists && previous != group {
			return fmt.Errorf("finalized source group number is reused: %w", core_errors.ErrConflict)
		}
		groupNumbers[group] = item.SourceGroupNumber
		groupsByNumber[item.SourceGroupNumber] = group
	}
	if len(usedFiles) != len(files) {
		return fmt.Errorf("finalized source file has no selected items: %w", core_errors.ErrConflict)
	}
	if len(groupNumbers) != finalized.GroupsCount {
		return fmt.Errorf("finalized group count differs: %w", core_errors.ErrConflict)
	}
	for number := 1; number <= finalized.GroupsCount; number++ {
		if _, exists := groupsByNumber[number]; !exists {
			return fmt.Errorf("finalized source group numbering has a gap: %w", core_errors.ErrConflict)
		}
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
		[]string{"batch_id", "position", "source_group_number", "payload"},
		pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			item := items[index]
			return []any{batchID, item.domain.Position,
				item.domain.SourceGroupNumber, string(item.payload)}, nil
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
