package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const errorFeedInsertBatchSize = 256

var _ cardpublication_monitoring.ErrorFeedRepository = (*MonitoringRepository)(nil)

func (repository *MonitoringRepository) LoadErrorCursor(
	ctx context.Context,
	sellerID core_domain.SellerID,
) (cardpublication_monitoring.ErrorCursor, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT seller_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at
		FROM wb.publication_error_cursors
		WHERE seller_id = $1;
	`
	cursor, err := scanErrorCursor(repository.pool.QueryRow(ctx, query, sellerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_monitoring.ErrorCursor{SellerID: sellerID}, nil
	}
	if err != nil {
		return cardpublication_monitoring.ErrorCursor{}, fmt.Errorf(
			"load publication error cursor: %w",
			err,
		)
	}
	return cursor, nil
}

func (repository *MonitoringRepository) SaveErrorFeed(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_monitoring.SaveErrorFeedCommand,
) (cardpublication_monitoring.ErrorCursor, error) {
	if tx == nil {
		return cardpublication_monitoring.ErrorCursor{}, errors.New(
			"save publication error feed: DBTX is nil",
		)
	}
	if err := ensureErrorCursor(ctx, tx, command.SellerID); err != nil {
		return cardpublication_monitoring.ErrorCursor{}, err
	}
	current, err := lockErrorCursor(ctx, tx, command.SellerID)
	if err != nil {
		return cardpublication_monitoring.ErrorCursor{}, err
	}
	if current.Revision != command.Previous.Revision ||
		!current.UpdatedAt.Equal(command.Previous.UpdatedAt) ||
		current.BatchUUID != command.Previous.BatchUUID {
		return cardpublication_monitoring.ErrorCursor{}, errors.New(
			"publication error cursor changed concurrently",
		)
	}

	inserted, err := insertErrorBatches(ctx, tx, command.Batches, command.PolledAt)
	if err != nil {
		return cardpublication_monitoring.ErrorCursor{}, err
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
		WHERE seller_id = $1
		RETURNING seller_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at;
	`
	saved, err := scanErrorCursor(tx.QueryRow(
		ctx,
		update,
		command.SellerID,
		cursorUpdatedAt,
		next.BatchUUID,
		inserted,
		command.PolledAt,
	))
	if err != nil {
		return cardpublication_monitoring.ErrorCursor{}, fmt.Errorf(
			"update publication error cursor: %w",
			err,
		)
	}
	return saved, nil
}

// insertErrorBatches сохраняет ограниченные порции и считает только новые записи.
func insertErrorBatches(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	batches []cardpublication_monitoring.ErrorBatchEvidence,
	observedAt time.Time,
) (int64, error) {
	var inserted int64
	for start := 0; start < len(batches); start += errorFeedInsertBatchSize {
		end := min(start+errorFeedInsertBatchSize, len(batches))
		var query strings.Builder
		query.WriteString(`
			INSERT INTO wb.publication_error_batches (
				seller_id, batch_uuid, batch_updated_at, vendor_codes,
				rejected_vendor_codes, error_codes, observed_at
			) VALUES
		`)
		arguments := make([]any, 0, (end-start)*7)
		for _, batch := range batches[start:end] {
			if len(arguments) > 0 {
				query.WriteByte(',')
			}
			parameter := len(arguments) + 1
			fmt.Fprintf(&query, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				parameter, parameter+1, parameter+2, parameter+3,
				parameter+4, parameter+5, parameter+6)
			arguments = append(arguments,
				batch.SellerID, batch.BatchUUID, batch.UpdatedAt,
				batch.VendorCodes, batch.RejectedVendorCodes, batch.ErrorCodes,
				observedAt)
		}
		query.WriteString(`
			ON CONFLICT (seller_id, batch_uuid, batch_updated_at) DO NOTHING;
		`)
		result, err := tx.Exec(ctx, query.String(), arguments...)
		if err != nil {
			return 0, fmt.Errorf("insert publication error batch: %w", err)
		}
		inserted += result.RowsAffected()
	}
	return inserted, nil
}

func (repository *MonitoringRepository) CaptureErrorBaseline(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_monitoring.CaptureErrorBaselineCommand,
) (cardpublication_monitoring.ErrorBaseline, error) {
	if tx == nil {
		return cardpublication_monitoring.ErrorBaseline{}, errors.New(
			"capture publication error baseline: DBTX is nil",
		)
	}
	if err := ensureErrorCursor(ctx, tx, command.SellerID); err != nil {
		return cardpublication_monitoring.ErrorBaseline{}, err
	}
	cursor, err := lockErrorCursor(ctx, tx, command.SellerID)
	if err != nil {
		return cardpublication_monitoring.ErrorBaseline{}, err
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
		  AND target.seller_id = $3;
	`
	var capturedAt time.Time
	if err := tx.QueryRow(
		ctx,
		validateAction,
		command.TransferID,
		command.ActionID,
		command.SellerID,
	).Scan(&capturedAt); err != nil {
		return cardpublication_monitoring.ErrorBaseline{}, fmt.Errorf(
			"validate publication error baseline action: %w",
			err,
		)
	}
	baseline := cardpublication_monitoring.ErrorBaseline{
		TransferID:      command.TransferID,
		ActionID:        command.ActionID,
		SellerID:        command.SellerID,
		CursorRevision:  cursor.Revision,
		CursorUpdatedAt: cursor.UpdatedAt,
		CursorBatchUUID: cursor.BatchUUID,
		CapturedAt:      capturedAt.UTC(),
	}
	if err := baseline.Validate(); err != nil {
		return cardpublication_monitoring.ErrorBaseline{}, err
	}
	return baseline, nil
}

func ensureErrorCursor(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sellerID core_domain.SellerID,
) error {
	const insert = `
		INSERT INTO wb.publication_error_cursors (seller_id)
		VALUES ($1)
		ON CONFLICT (seller_id) DO NOTHING;
	`
	if _, err := tx.Exec(ctx, insert, sellerID); err != nil {
		return fmt.Errorf("ensure publication error cursor: %w", err)
	}
	return nil
}

func lockErrorCursor(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sellerID core_domain.SellerID,
) (cardpublication_monitoring.ErrorCursor, error) {
	const query = `
		SELECT seller_id, cursor_updated_at, cursor_batch_uuid, revision, polled_at
		FROM wb.publication_error_cursors
		WHERE seller_id = $1
		FOR UPDATE;
	`
	cursor, err := scanErrorCursor(tx.QueryRow(ctx, query, sellerID))
	if err != nil {
		return cardpublication_monitoring.ErrorCursor{}, fmt.Errorf(
			"lock publication error cursor: %w",
			err,
		)
	}
	return cursor, nil
}

func scanErrorCursor(row pgx.Row) (
	cardpublication_monitoring.ErrorCursor,
	error,
) {
	var cursor cardpublication_monitoring.ErrorCursor
	var updatedAt, polledAt pgtype.Timestamptz
	if err := row.Scan(
		&cursor.SellerID,
		&updatedAt,
		&cursor.BatchUUID,
		&cursor.Revision,
		&polledAt,
	); err != nil {
		return cardpublication_monitoring.ErrorCursor{}, err
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
