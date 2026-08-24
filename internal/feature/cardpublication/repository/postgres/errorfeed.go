package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *Repository) LoadErrorCursor(
	ctx context.Context,
	cabinetID cardpublication_service.CabinetID,
) (cardpublication_service.ErrorCursor, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		SELECT cabinet_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at
		FROM wb.publication_error_cursors
		WHERE cabinet_id = $1;
	`
	cursor, err := scanErrorCursor(repository.pool.QueryRow(ctx, query, cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_service.ErrorCursor{CabinetID: cabinetID}, nil
	}
	if err != nil {
		return cardpublication_service.ErrorCursor{}, fmt.Errorf(
			"load publication error cursor: %w",
			err,
		)
	}
	return cursor, nil
}

func (repository *Repository) SaveErrorFeed(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_service.SaveErrorFeedCommand,
) (cardpublication_service.ErrorCursor, error) {
	if tx == nil {
		return cardpublication_service.ErrorCursor{}, errors.New(
			"save publication error feed: DBTX is nil",
		)
	}
	if err := ensureErrorCursor(ctx, tx, command.CabinetID); err != nil {
		return cardpublication_service.ErrorCursor{}, err
	}
	current, err := lockErrorCursor(ctx, tx, command.CabinetID)
	if err != nil {
		return cardpublication_service.ErrorCursor{}, err
	}
	if current.Revision != command.Previous.Revision ||
		!current.UpdatedAt.Equal(command.Previous.UpdatedAt) ||
		current.BatchUUID != command.Previous.BatchUUID {
		return cardpublication_service.ErrorCursor{}, errors.New(
			"publication error cursor changed concurrently",
		)
	}

	inserted := int64(0)
	for _, batch := range command.Batches {
		const insert = `
			INSERT INTO wb.publication_error_batches (
				cabinet_id,
				batch_uuid,
				batch_updated_at,
				source_digest,
				vendor_codes,
				rejected_vendor_codes,
				error_codes,
				observed_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (cabinet_id, batch_uuid, source_digest) DO NOTHING;
		`
		result, err := tx.Exec(
			ctx,
			insert,
			batch.CabinetID,
			batch.BatchUUID,
			batch.UpdatedAt,
			batch.SourceDigest[:],
			batch.VendorCodes,
			batch.RejectedVendorCodes,
			batch.ErrorCodes,
			command.PolledAt,
		)
		if err != nil {
			return cardpublication_service.ErrorCursor{}, fmt.Errorf(
				"insert publication error batch: %w",
				err,
			)
		}
		inserted += result.RowsAffected()
	}
	next := command.Next
	if cursorAfterStored(
		current.UpdatedAt,
		current.BatchUUID,
		next.UpdatedAt,
		next.BatchUUID,
	) {
		next.UpdatedAt = current.UpdatedAt
		next.BatchUUID = current.BatchUUID
	}
	var cursorUpdatedAt any
	if !next.UpdatedAt.IsZero() {
		cursorUpdatedAt = next.UpdatedAt
	}
	const update = `
		UPDATE wb.publication_error_cursors
		SET cursor_updated_at = $2,
		    cursor_batch_uuid = $3,
		    revision = revision + $4,
		    polled_at = $5,
		    updated_at = CURRENT_TIMESTAMP
		WHERE cabinet_id = $1
		RETURNING cabinet_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at;
	`
	saved, err := scanErrorCursor(tx.QueryRow(
		ctx,
		update,
		command.CabinetID,
		cursorUpdatedAt,
		next.BatchUUID,
		inserted,
		command.PolledAt,
	))
	if err != nil {
		return cardpublication_service.ErrorCursor{}, fmt.Errorf(
			"update publication error cursor: %w",
			err,
		)
	}
	return saved, nil
}

func (repository *Repository) CaptureErrorBaseline(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_service.CaptureErrorBaselineCommand,
) (cardpublication_service.ErrorBaseline, error) {
	if tx == nil {
		return cardpublication_service.ErrorBaseline{}, errors.New(
			"capture publication error baseline: DBTX is nil",
		)
	}
	if err := ensureErrorCursor(ctx, tx, command.CabinetID); err != nil {
		return cardpublication_service.ErrorBaseline{}, err
	}
	cursor, err := lockErrorCursor(ctx, tx, command.CabinetID)
	if err != nil {
		return cardpublication_service.ErrorBaseline{}, err
	}
	const validateAction = `
		SELECT CURRENT_TIMESTAMP
		FROM wb.publication_actions AS action
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		WHERE action.transfer_id = $1
		  AND action.id = $2
		  AND action.state = 'planned'
		  AND target.cabinet_id = $3;
	`
	var capturedAt time.Time
	if err := tx.QueryRow(
		ctx,
		validateAction,
		command.TransferID,
		command.ActionID,
		command.CabinetID,
	).Scan(&capturedAt); err != nil {
		return cardpublication_service.ErrorBaseline{}, fmt.Errorf(
			"validate publication error baseline action: %w",
			err,
		)
	}
	baseline := cardpublication_service.ErrorBaseline{
		TransferID:      command.TransferID,
		ActionID:        command.ActionID,
		CabinetID:       command.CabinetID,
		CursorRevision:  cursor.Revision,
		CursorUpdatedAt: cursor.UpdatedAt,
		CursorBatchUUID: cursor.BatchUUID,
		CapturedAt:      capturedAt.UTC(),
	}
	if err := baseline.Validate(); err != nil {
		return cardpublication_service.ErrorBaseline{}, err
	}
	return baseline, nil
}

func ensureErrorCursor(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	cabinetID cardpublication_service.CabinetID,
) error {
	const insert = `
		INSERT INTO wb.publication_error_cursors (cabinet_id)
		VALUES ($1)
		ON CONFLICT (cabinet_id) DO NOTHING;
	`
	if _, err := tx.Exec(ctx, insert, cabinetID); err != nil {
		return fmt.Errorf("ensure publication error cursor: %w", err)
	}
	return nil
}

func lockErrorCursor(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	cabinetID cardpublication_service.CabinetID,
) (cardpublication_service.ErrorCursor, error) {
	const query = `
		SELECT cabinet_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at
		FROM wb.publication_error_cursors
		WHERE cabinet_id = $1
		FOR UPDATE;
	`
	cursor, err := scanErrorCursor(tx.QueryRow(ctx, query, cabinetID))
	if err != nil {
		return cardpublication_service.ErrorCursor{}, fmt.Errorf(
			"lock publication error cursor: %w",
			err,
		)
	}
	return cursor, nil
}

func scanErrorCursor(row interface{ Scan(...any) error }) (
	cardpublication_service.ErrorCursor,
	error,
) {
	var cursor cardpublication_service.ErrorCursor
	var updatedAt, polledAt pgtype.Timestamptz
	if err := row.Scan(
		&cursor.CabinetID,
		&updatedAt,
		&cursor.BatchUUID,
		&cursor.Revision,
		&polledAt,
	); err != nil {
		return cardpublication_service.ErrorCursor{}, err
	}
	if updatedAt.Valid {
		cursor.UpdatedAt = updatedAt.Time.UTC()
	}
	if polledAt.Valid {
		cursor.PolledAt = polledAt.Time.UTC()
	}
	return cursor, nil
}

func cursorAfterStored(leftTime time.Time, leftUUID string, rightTime time.Time, rightUUID string) bool {
	if leftTime.After(rightTime) {
		return true
	}
	return leftTime.Equal(rightTime) && leftUUID > rightUUID
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

var _ cardpublication_service.ErrorFeedRepository = (*Repository)(nil)
