package cardimport_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

// ContinueAfterErrors keeps safely parsed cards and removes data involved in
// blocking issues. The session stays collecting, so an entirely invalid file
// can be replaced without cancelling the whole upload.
func (r *Repository) ContinueAfterErrors(
	ctx context.Context,
	command cardimport_service.ContinueCommand,
) (cardimport_service.Session, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return cardimport_service.Session{}, fmt.Errorf(
			"begin continue cardimport session: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQuery = `
		SELECT ` + sessionColumns + `
		FROM wb.card_import_sessions
		WHERE id = $1
			AND author_telegram_id = $2
			AND status = 'collecting'
			AND revision = $3
			AND EXISTS (
				SELECT 1
				FROM wb.card_import_issues
				WHERE session_id = $1 AND severity = 'error'
			)
		FOR UPDATE;
	`
	var locked sessionModel
	if err := locked.Scan(tx.QueryRow(
		ctx,
		lockQuery,
		command.SessionID,
		command.AuthorTelegramID,
		command.ExpectedRevision,
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.Session{}, fmt.Errorf(
				"cardimport session is stale or has no errors: %w",
				core_errors.ErrConflict,
			)
		}
		return cardimport_service.Session{}, fmt.Errorf(
			"lock cardimport session for continue: %w",
			err,
		)
	}

	statements := []string{
		// A row-level error only removes a card built from that row. A file-level
		// error has no row number and therefore makes the whole file unusable.
		`DELETE FROM wb.card_import_items AS item
		 USING wb.card_import_files AS file
		 WHERE item.session_id = $1
		   AND file.id = item.source_file_id
		   AND file.status = 'invalid'
		   AND EXISTS (
		       SELECT 1
		       FROM wb.card_import_issues AS issue
		       WHERE issue.session_id = $1
		         AND issue.file_id = file.id
		         AND issue.severity = 'error'
		         AND (
		             issue.row_number IS NULL
		             OR issue.row_number = ANY(item.source_rows)
		         )
		   );`,
		// For duplicates, deterministically keep the first uploaded card.
		`WITH ranked AS (
		    SELECT
		        id,
		        row_number() OVER (
		            PARTITION BY vendor_code
		            ORDER BY source_file_id, position, id
		        ) AS duplicate_position
		    FROM wb.card_import_items
		    WHERE session_id = $1
		), losers AS (
		    SELECT id FROM ranked WHERE duplicate_position > 1
		)
		DELETE FROM wb.card_import_items AS item
		USING losers
		WHERE item.id = losers.id;`,
		`WITH ranked AS (
		    SELECT
		        item.id,
		        row_number() OVER (
		            PARTITION BY barcode
		            ORDER BY item.source_file_id, item.position, item.id
		        ) AS duplicate_position
		    FROM wb.card_import_items AS item
		    CROSS JOIN LATERAL unnest(item.barcodes) AS barcode
		    WHERE item.session_id = $1
		), losers AS (
		    SELECT DISTINCT id FROM ranked WHERE duplicate_position > 1
		)
		DELETE FROM wb.card_import_items AS item
		USING losers
		WHERE item.id = losers.id;`,
		`UPDATE wb.card_import_files AS file
		 SET
		     status = CASE
		         WHEN EXISTS (
		             SELECT 1
		             FROM wb.card_import_items AS item
		             WHERE item.source_file_id = file.id
		         ) THEN 'valid'
		         ELSE 'abandoned'
		     END,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE file.session_id = $1
		   AND file.status IN ('valid', 'invalid');`,
		`DELETE FROM wb.card_import_issues
		 WHERE session_id = $1 AND severity = 'error';`,
		`DELETE FROM wb.card_import_issues AS issue
		 USING wb.card_import_files AS file
		 WHERE issue.file_id = file.id
		   AND file.session_id = $1
		   AND file.status = 'abandoned';`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, command.SessionID); err != nil {
			return cardimport_service.Session{}, fmt.Errorf(
				"skip invalid cardimport data: %w",
				err,
			)
		}
	}

	if err := rebuildCrossFileIssues(ctx, tx, command.SessionID); err != nil {
		return cardimport_service.Session{}, err
	}

	const updateQuery = `
		UPDATE wb.card_import_sessions
		SET revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND revision = $2 AND status = 'collecting'
		RETURNING ` + sessionColumns + `;
	`
	var updated sessionModel
	if err := updated.Scan(tx.QueryRow(
		ctx,
		updateQuery,
		command.SessionID,
		command.ExpectedRevision,
	)); err != nil {
		return cardimport_service.Session{}, fmt.Errorf(
			"update continued cardimport session: %w",
			err,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return cardimport_service.Session{}, fmt.Errorf(
			"commit continued cardimport session: %w",
			err,
		)
	}
	return updated.domain(), nil
}
