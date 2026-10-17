package cardimport_postgres_repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

type lockedFinalizeSession struct {
	authorUserID           *int64
	authorTelegramID       int64
	purpose                cardimport_service.Purpose
	status                 cardimport_service.SessionStatus
	revision               int64
	finalizedBatchID       *int64
	finalizedFromRevision  *int64
	finalizeIdempotencyKey *string
	finalizeCommandDigest  []byte
}

type frozenBatchItem struct {
	domain  cardimport_service.BatchItem
	payload []byte
}

// Finalize финализирует сессию импорта карточек, создавая неизменяемый батч.
func (r *Repository) Finalize(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	user domain.User,
	command cardimport_service.FinalizeCommand,
	commandDigest cardimport_service.Digest,
) (cardimport_service.BatchHeader, error) {
	if tx == nil {
		return cardimport_service.BatchHeader{}, errors.New(
			"finalize cardimport session: DBTX is nil",
		)
	}

	session, err := lockFinalizeSession(ctx, tx, command.SessionID)
	if err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	if session.authorTelegramID != user.TelegramID {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"cardimport session ID='%d' is owned by another user: %w",
			command.SessionID,
			core_errors.ErrForbidden,
		)
	}

	if session.status == cardimport_service.SessionStatusFinalized {
		if session.finalizedBatchID == nil ||
			session.finalizedFromRevision == nil ||
			session.finalizeIdempotencyKey == nil ||
			*session.finalizedFromRevision != command.ExpectedRevision ||
			*session.finalizeIdempotencyKey != command.IdempotencyKey ||
			!bytes.Equal(session.finalizeCommandDigest, commandDigest[:]) {
			return cardimport_service.BatchHeader{}, fmt.Errorf(
				"cardimport finalize command does not match stored command: %w",
				core_errors.ErrConflict,
			)
		}

		return loadBatch(ctx, tx, cardimport_service.BatchID(
			*session.finalizedBatchID,
		))
	}

	if session.status != cardimport_service.SessionStatusCollecting {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"cardimport session status '%s' cannot be finalized: %w",
			session.status,
			core_errors.ErrConflict,
		)
	}
	if session.revision != command.ExpectedRevision {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"cardimport session revision changed from '%d' to '%d': %w",
			command.ExpectedRevision,
			session.revision,
			core_errors.ErrConflict,
		)
	}

	if err := validateFinalizeReadiness(ctx, tx, command.SessionID); err != nil {
		return cardimport_service.BatchHeader{}, err
	}

	items, groupsCount, err := loadFrozenItems(ctx, tx, command.SessionID)
	if err != nil {
		return cardimport_service.BatchHeader{}, err
	}
	checksum, err := cardimport_service.BatchChecksum(
		session.purpose,
		groupsCount,
		batchItemDomains(items),
	)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"calculate cardimport batch checksum: %w",
			err,
		)
	}

	snapshot := cardimport_service.AuthorSnapshot{
		UserID:         session.authorUserID,
		TelegramUserID: user.TelegramID,
		DisplayName:    user.FullName,
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"marshal cardimport author snapshot: %w",
			err,
		)
	}

	const insertBatch = `
		INSERT INTO wb.card_batches (
			source_session_id,
			source_kind,
			source_reference_id,
			purpose,
			schema_version,
			normalization_version,
			items_count,
			groups_count,
			checksum,
			author_snapshot
		)
		VALUES ($1, 'xlsx', $1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		RETURNING id, finalized_at;
	`
	var (
		batchID     int64
		finalizedAt time.Time
	)
	if err := tx.QueryRow(
		ctx,
		insertBatch,
		command.SessionID,
		session.purpose,
		cardimport_service.BatchSchemaVersion,
		cardimport_service.BatchNormalizationVersion,
		len(items),
		groupsCount,
		checksum[:],
		string(snapshotJSON),
	).Scan(&batchID, &finalizedAt); err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"insert cardimport batch: %w",
			err,
		)
	}

	if err := copyFrozenItems(
		ctx,
		tx,
		cardimport_service.BatchID(batchID),
		items,
	); err != nil {
		return cardimport_service.BatchHeader{}, err
	}

	const finalizeSession = `
		UPDATE wb.card_import_sessions
		SET
			status = 'finalized',
			revision = revision + 1,
			finalized_batch_id = $2,
			finalized_from_revision = $3,
			finalize_idempotency_key = $4,
			finalize_command_digest = $5,
			finalized_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND status = 'collecting'
			AND revision = $3;
	`
	result, err := tx.Exec(
		ctx,
		finalizeSession,
		command.SessionID,
		batchID,
		command.ExpectedRevision,
		command.IdempotencyKey,
		commandDigest[:],
	)
	if err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"mark cardimport session finalized: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"cardimport session changed while finalizing: %w",
			core_errors.ErrConflict,
		)
	}

	header := cardimport_service.BatchHeader{
		ID:                   cardimport_service.BatchID(batchID),
		SourceSessionID:      command.SessionID,
		Purpose:              session.purpose,
		ItemsCount:           len(items),
		GroupsCount:          groupsCount,
		SchemaVersion:        cardimport_service.BatchSchemaVersion,
		NormalizationVersion: cardimport_service.BatchNormalizationVersion,
		Checksum:             checksum,
		AuthorSnapshot:       snapshot,
		FinalizedAt:          finalizedAt,
	}
	if err := header.Validate(); err != nil {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"validate finalized cardimport batch: %w",
			err,
		)
	}

	return header, nil
}

// lockFinalizeSession блокирует сессию импорта для финализации.
func lockFinalizeSession(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sessionID cardimport_service.SessionID,
) (lockedFinalizeSession, error) {
	const query = `
		SELECT
			author_user_id,
			author_telegram_id,
			purpose,
			status,
			revision,
			finalized_batch_id,
			finalized_from_revision,
			finalize_idempotency_key,
			finalize_command_digest
		FROM wb.card_import_sessions
		WHERE id = $1
		FOR UPDATE;
	`

	var session lockedFinalizeSession
	err := tx.QueryRow(ctx, query, sessionID).Scan(
		&session.authorUserID,
		&session.authorTelegramID,
		&session.purpose,
		&session.status,
		&session.revision,
		&session.finalizedBatchID,
		&session.finalizedFromRevision,
		&session.finalizeIdempotencyKey,
		&session.finalizeCommandDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedFinalizeSession{}, fmt.Errorf(
			"cardimport session ID='%d': %w",
			sessionID,
			core_errors.ErrNotFound,
		)
	}
	if err != nil {
		return lockedFinalizeSession{}, fmt.Errorf(
			"lock cardimport session for finalize: %w",
			err,
		)
	}

	return session, nil
}

// validateFinalizeReadiness проверяет готовность сессии к финализации (отсутствие ошибок и наличие валидных файлов).
func validateFinalizeReadiness(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sessionID cardimport_service.SessionID,
) error {
	const query = `
		SELECT
			COUNT(*) FILTER (WHERE status <> 'abandoned'),
			COUNT(*) FILTER (WHERE status = 'valid'),
			(
				SELECT COUNT(*)
				FROM wb.card_import_issues
				WHERE session_id = $1 AND severity = 'error'
			),
			(
				SELECT COUNT(*)
				FROM wb.card_import_items
				WHERE session_id = $1
			)
		FROM wb.card_import_files
		WHERE session_id = $1;
	`

	var activeFiles, validFiles, errorsCount, itemsCount int64
	if err := tx.QueryRow(ctx, query, sessionID).Scan(
		&activeFiles,
		&validFiles,
		&errorsCount,
		&itemsCount,
	); err != nil {
		return fmt.Errorf("check cardimport finalize readiness: %w", err)
	}

	switch {
	case activeFiles == 0:
		return fmt.Errorf(
			"cardimport session has no active files: %w",
			core_errors.ErrConflict,
		)
	case activeFiles != validFiles:
		return fmt.Errorf(
			"not all cardimport files are valid: %w",
			core_errors.ErrConflict,
		)
	case errorsCount != 0:
		return fmt.Errorf(
			"cardimport session has blocking issues: %w",
			core_errors.ErrConflict,
		)
	case itemsCount == 0:
		return fmt.Errorf(
			"cardimport session has no items: %w",
			core_errors.ErrConflict,
		)
	default:
		return nil
	}
}

// loadFrozenItems загружает элементы сессии для фиксации в батче.
func loadFrozenItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sessionID cardimport_service.SessionID,
) ([]frozenBatchItem, int, error) {
	const query = `
		SELECT
			source_file_id,
			source_rows,
			group_value,
			vendor_code,
			barcodes,
			payload
		FROM wb.card_import_items
		WHERE session_id = $1
		ORDER BY source_file_id, position;
	`

	rows, err := tx.Query(ctx, query, sessionID)
	if err != nil {
		return nil, 0, fmt.Errorf("select cardimport items to freeze: %w", err)
	}
	defer rows.Close()

	items := make([]frozenBatchItem, 0)
	groups := make(map[cardimport_service.SourceGroupKey]struct{})
	barcodes := make(map[string]int)
	for rows.Next() {
		var (
			sourceFileID int64
			sourceRows   []int32
			group        string
			vendorCode   string
			storedCodes  []string
			payload      []byte
		)
		if err := rows.Scan(
			&sourceFileID,
			&sourceRows,
			&group,
			&vendorCode,
			&storedCodes,
			&payload,
		); err != nil {
			return nil, 0, fmt.Errorf(
				"scan cardimport item to freeze: %w",
				err,
			)
		}

		var card cardimport_service.AggregatedCard
		if err := json.Unmarshal(payload, &card); err != nil {
			return nil, 0, fmt.Errorf(
				"decode cardimport item payload: %w",
				err,
			)
		}
		itemRows := make([]int, len(sourceRows))
		for index, row := range sourceRows {
			itemRows[index] = int(row)
		}
		if card.Group != group ||
			card.Variant.VendorCode != vendorCode ||
			!reflect.DeepEqual(card.SourceRows, itemRows) ||
			!reflect.DeepEqual(card.Barcodes(), storedCodes) {
			return nil, 0, fmt.Errorf(
				"cardimport item columns differ from typed payload: %w",
				core_errors.ErrConflict,
			)
		}

		position := len(items) + 1
		for _, barcode := range storedCodes {
			if barcode == "" {
				continue
			}
			if previous, exists := barcodes[barcode]; exists {
				return nil, 0, fmt.Errorf(
					"barcode is duplicated at positions %d and %d: %w",
					previous,
					position,
					core_errors.ErrConflict,
				)
			}
			barcodes[barcode] = position
		}

		groupKey := cardimport_service.NewSourceGroupKey(
			cardimport_service.FileID(sourceFileID),
			group,
			vendorCode,
		)
		groups[groupKey] = struct{}{}
		canonicalPayload, err := json.Marshal(card)
		if err != nil {
			return nil, 0, fmt.Errorf(
				"encode frozen cardimport payload: %w",
				err,
			)
		}
		items = append(items, frozenBatchItem{
			domain: cardimport_service.BatchItem{
				Position:       position,
				SourceFileID:   cardimport_service.FileID(sourceFileID),
				SourceRows:     itemRows,
				SourceGroupKey: groupKey,
				VendorCode:     vendorCode,
				Payload:        card,
			},
			payload: canonicalPayload,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf(
			"iterate cardimport items to freeze: %w",
			err,
		)
	}
	if len(items) == 0 {
		return nil, 0, fmt.Errorf(
			"cardimport session has no items to freeze: %w",
			core_errors.ErrConflict,
		)
	}

	return items, len(groups), nil
}

// batchItemDomains преобразует срез замороженных элементов в доменные модели BatchItem.
func batchItemDomains(items []frozenBatchItem) []cardimport_service.BatchItem {
	result := make([]cardimport_service.BatchItem, len(items))
	for index := range items {
		result[index] = items[index].domain
	}
	return result
}

// copyFrozenItems выполняет массовую вставку элементов в card_batch_items через CopyFrom.
func copyFrozenItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	batchID cardimport_service.BatchID,
	items []frozenBatchItem,
) error {
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "card_batch_items"},
		[]string{
			"batch_id",
			"position",
			"source_file_id",
			"source_rows",
			"source_group_key",
			"vendor_code",
			"payload",
		},
		pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			item := items[index]
			rows := make([]int32, len(item.domain.SourceRows))
			for rowIndex, row := range item.domain.SourceRows {
				rows[rowIndex] = int32(row)
			}
			return []any{
				batchID,
				item.domain.Position,
				item.domain.SourceFileID,
				rows,
				item.domain.SourceGroupKey[:],
				item.domain.VendorCode,
				string(item.payload),
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy frozen cardimport batch items: %w", err)
	}
	if count != int64(len(items)) {
		return fmt.Errorf(
			"copied %d of %d cardimport batch items: %w",
			count,
			len(items),
			core_errors.ErrConflict,
		)
	}

	return nil
}
