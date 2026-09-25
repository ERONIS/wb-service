package cardimport_postgres_repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

const batchColumns = `
	batch.id,
	COALESCE(batch.source_session_id, batch.source_reference_id),
	batch.purpose,
	batch.items_count,
	batch.groups_count,
	batch.schema_version,
	batch.normalization_version,
	batch.checksum,
	batch.author_snapshot,
	batch.finalized_at
`

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

func (r *Repository) Finalize(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	actor cardimport_service.TrustedActor,
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
	if session.authorTelegramID != actor.TelegramUserID {
		return cardimport_service.BatchHeader{}, fmt.Errorf(
			"cardimport session ID='%d' is owned by another actor: %w",
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
		TelegramUserID: actor.TelegramUserID,
		DisplayName:    actor.DisplayName,
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

func batchItemDomains(items []frozenBatchItem) []cardimport_service.BatchItem {
	result := make([]cardimport_service.BatchItem, len(items))
	for index := range items {
		result[index] = items[index].domain
	}
	return result
}

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

func (r *Repository) GetBatch(
	ctx context.Context,
	batchID cardimport_service.BatchID,
) (cardimport_service.BatchHeader, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	return loadBatch(ctx, r.pool, batchID)
}

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

func loadBatch(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	batchID cardimport_service.BatchID,
) (cardimport_service.BatchHeader, error) {
	query := `
		SELECT ` + batchColumns + `
		FROM wb.card_batches AS batch
		LEFT JOIN wb.card_import_sessions AS session
			ON session.finalized_batch_id = batch.id
		   AND session.id = batch.source_session_id
		WHERE batch.id = $1
		  AND (
			(batch.source_kind = 'xlsx' AND session.status = 'finalized')
			OR batch.source_kind = 'wb_cabinet'
		  );
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
		&header.SchemaVersion,
		&header.NormalizationVersion,
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
			COALESCE(item.source_file_id, batch.source_reference_id),
			item.source_rows,
			item.source_group_key,
			item.vendor_code,
			item.payload
		FROM wb.card_batch_items AS item
		JOIN wb.card_batches AS batch ON batch.id = item.batch_id
		LEFT JOIN wb.card_import_sessions AS session
			ON session.finalized_batch_id = batch.id
		   AND session.id = batch.source_session_id
		WHERE item.batch_id = $1
			AND item.position > $2
			AND (
				(batch.source_kind = 'xlsx' AND session.status = 'finalized')
				OR batch.source_kind = 'wb_cabinet'
			)
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
