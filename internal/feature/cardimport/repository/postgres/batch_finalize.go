package cardimport_postgres_repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

type frozenBatchItem struct {
	domain  cardimport_service.BatchItem
	payload []byte
}

func (r *Repository) PersistFinalized(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	finalized cardimport_service.FinalizedSession,
) (cardimport_service.BatchHeader, error) {
	if tx == nil {
		return cardimport_service.BatchHeader{}, errors.New("persist finalized cardimport: DBTX is nil")
	}
	if err := validateFinalizedSession(finalized); err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	const insertSession = `
		INSERT INTO wb.card_import_sessions (
			id, author_user_id, author_telegram_id, purpose, status,
			revision, created_at, updated_at
		) VALUES (
			$1, (SELECT id FROM wb.users WHERE tg_id = $2), $2, $3, 'collecting',
			$4, $5, $6
		)
		ON CONFLICT (id) DO NOTHING
		RETURNING author_user_id;
	`
	var authorUserID *int64
	err := tx.QueryRow(ctx, insertSession,
		finalized.Session.ID, finalized.User.TelegramID, finalized.Session.Purpose,
		finalized.Session.Revision, finalized.Session.CreatedAt, finalized.Session.UpdatedAt,
	).Scan(&authorUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return loadExistingFinalization(ctx, tx, finalized)
	}
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("insert finalized cardimport session: %w", err)
	}
	for _, file := range finalized.Files {
		sha, err := hex.DecodeString(file.SHA256)
		if err != nil {
			return cardimport_service.BatchHeader{}, fmt.Errorf("decode file SHA-256: %w", err)
		}
		const insertFile = `
			INSERT INTO wb.card_import_files (
				id, session_id, telegram_file_id, telegram_file_unique_id,
				telegram_message_id, original_filename, mime_type, declared_size,
				stored_size, sha256, status, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'valid',$11,$12);
		`
		if _, err := tx.Exec(ctx, insertFile, file.ID, file.SessionID,
			file.TelegramFileID, file.TelegramFileUniqueID, file.TelegramMessageID,
			file.OriginalFilename, file.MIMEType, file.DeclaredSize,
			file.StoredSize, sha, file.CreatedAt, file.UpdatedAt,
		); err != nil {
			return cardimport_service.BatchHeader{}, fmt.Errorf("insert finalized cardimport file: %w", err)
		}
	}
	snapshot := cardimport_service.AuthorSnapshot{
		UserID: authorUserID, TelegramUserID: finalized.User.TelegramID,
		DisplayName: finalized.User.FullName,
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("marshal cardimport author snapshot: %w", err)
	}
	const insertBatch = `
		INSERT INTO wb.card_batches (
			source_session_id, source_kind, source_reference_id, purpose,
			items_count, groups_count,
			checksum, author_snapshot
		) VALUES ($1,'xlsx',$1,$2,$3,$4,$5,$6::jsonb)
		RETURNING id, finalized_at;
	`
	var batchID int64
	var finalizedAt time.Time
	err = tx.QueryRow(ctx, insertBatch,
		finalized.Session.ID, finalized.Session.Purpose,
		len(finalized.Items), finalized.GroupsCount, finalized.Checksum[:], string(snapshotJSON),
	).Scan(&batchID, &finalizedAt)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("insert cardimport batch: %w", err)
	}
	items := make([]frozenBatchItem, len(finalized.Items))
	for index, item := range finalized.Items {
		payload, err := json.Marshal(item.Payload)
		if err != nil {
			return cardimport_service.BatchHeader{}, fmt.Errorf("marshal cardimport item: %w", err)
		}
		items[index] = frozenBatchItem{domain: item, payload: payload}
	}
	if err := copyFrozenItems(ctx, tx, cardimport_service.BatchID(batchID), items); err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	const finalizeSession = `
		UPDATE wb.card_import_sessions
		SET status = 'finalized', revision = revision + 1,
			finalized_batch_id = $2, finalized_from_revision = $3,
			finalize_idempotency_key = $4, finalize_command_digest = $5,
			finalized_at = $6, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'collecting' AND revision = $3;
	`
	tag, err := tx.Exec(ctx, finalizeSession,
		finalized.Session.ID, batchID, finalized.Command.ExpectedRevision,
		finalized.Command.IdempotencyKey, finalized.CommandDigest[:], finalizedAt,
	)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("mark cardimport session finalized: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return cardimport_service.BatchHeader{}, fmt.Errorf("cardimport session changed while finalizing: %w", core_errors.ErrConflict)
	}
	header := cardimport_service.BatchHeader{
		ID: cardimport_service.BatchID(batchID), SourceSessionID: finalized.Session.ID,
		Purpose: finalized.Session.Purpose, ItemsCount: len(items), GroupsCount: finalized.GroupsCount,
		Checksum: finalized.Checksum, AuthorSnapshot: snapshot, FinalizedAt: finalizedAt,
	}
	if err := header.Validate(); err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	return header, nil
}

func validateFinalizedSession(finalized cardimport_service.FinalizedSession) error {
	if err := finalized.Session.Validate(); err != nil {
		return err
	}
	if err := finalized.User.Validate(); err != nil {
		return err
	}
	if finalized.Session.Status != cardimport_service.SessionStatusCollecting ||
		finalized.Session.AuthorTelegramID != finalized.User.TelegramID ||
		finalized.Session.Revision != finalized.Command.ExpectedRevision ||
		finalized.Session.ID != finalized.Command.SessionID ||
		len(finalized.Files) == 0 || len(finalized.Items) == 0 {
		return fmt.Errorf("invalid finalized cardimport session: %w", core_errors.ErrConflict)
	}
	files := make(map[cardimport_service.FileID]struct{}, len(finalized.Files))
	for _, file := range finalized.Files {
		if err := file.Validate(); err != nil {
			return err
		}
		if file.SessionID != finalized.Session.ID || file.Status != cardimport_service.FileStatusValid {
			return fmt.Errorf("invalid finalized cardimport file: %w", core_errors.ErrConflict)
		}
		files[file.ID] = struct{}{}
	}
	groups := make(map[cardimport_service.SourceGroupKey]struct{})
	for index, item := range finalized.Items {
		if item.Position != index+1 {
			return fmt.Errorf("invalid finalized item position: %w", core_errors.ErrConflict)
		}
		if _, exists := files[item.SourceFileID]; !exists {
			return fmt.Errorf("finalized item source file is missing: %w", core_errors.ErrConflict)
		}
		groups[item.SourceGroupKey] = struct{}{}
	}
	if len(groups) != finalized.GroupsCount {
		return fmt.Errorf("finalized group count differs: %w", core_errors.ErrConflict)
	}
	checksum, err := cardimport_service.BatchChecksum(finalized.Session.Purpose, finalized.GroupsCount, finalized.Items)
	if err != nil {
		return err
	}
	if checksum != finalized.Checksum {
		return fmt.Errorf("finalized checksum differs: %w", core_errors.ErrConflict)
	}
	return nil
}

func loadExistingFinalization(
	ctx context.Context, tx core_postgres_transaction.DBTX,
	finalized cardimport_service.FinalizedSession,
) (cardimport_service.BatchHeader, error) {
	const query = `
		SELECT author_telegram_id, finalized_batch_id, finalized_from_revision,
			finalize_idempotency_key, finalize_command_digest
		FROM wb.card_import_sessions
		WHERE id = $1 AND status = 'finalized' FOR UPDATE;
	`
	var authorID int64
	var batchID int64
	var revision int64
	var key string
	var digest []byte
	if err := tx.QueryRow(ctx, query, finalized.Session.ID).Scan(
		&authorID, &batchID, &revision, &key, &digest,
	); err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf("cardimport session ID collision: %w", core_errors.ErrConflict)
	}
	if authorID != finalized.User.TelegramID || revision != finalized.Command.ExpectedRevision ||
		key != finalized.Command.IdempotencyKey || !bytes.Equal(digest, finalized.CommandDigest[:]) {
		return cardimport_service.BatchHeader{}, fmt.Errorf("cardimport finalize command differs: %w", core_errors.ErrConflict)
	}
	header, err := loadBatch(ctx, tx, cardimport_service.BatchID(batchID))
	if err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	if header.Checksum != finalized.Checksum {
		return cardimport_service.BatchHeader{}, fmt.Errorf("cardimport finalized payload differs: %w", core_errors.ErrConflict)
	}
	return header, nil
}

func copyFrozenItems(
	ctx context.Context, tx core_postgres_transaction.DBTX,
	batchID cardimport_service.BatchID, items []frozenBatchItem,
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
