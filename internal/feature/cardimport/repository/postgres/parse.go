package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) ClaimFileForParsing(
	ctx context.Context,
	command cardimport_service.ParseFileCommand,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	const query = `
		WITH locked_session AS MATERIALIZED (
			SELECT id
			FROM wb.card_import_sessions
			WHERE id = $2
				AND author_telegram_id = $3
				AND status = 'collecting'
			FOR UPDATE
		),
		claimed AS (
			UPDATE wb.card_import_files AS file
			SET
				status = 'parsing',
				updated_at = CURRENT_TIMESTAMP
			FROM locked_session AS session
			WHERE file.id = $1
				AND file.session_id = $2
				AND session.id = file.session_id
				AND (
					file.status IN ('stored', 'valid', 'invalid')
					OR (
						file.status = 'parsing'
						AND file.updated_at < CURRENT_TIMESTAMP - INTERVAL '5 minutes'
					)
				)
			RETURNING file.content
		)
		SELECT content
		FROM claimed;
	`

	var content []byte
	if err := r.pool.QueryRow(
		ctx,
		query,
		command.FileID,
		command.SessionID,
		command.AuthorTelegramID,
	).Scan(&content); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf(
				"cardimport file is unavailable for parsing: %w",
				core_errors.ErrConflict,
			)
		}

		return nil, fmt.Errorf("load cardimport file content: %w", err)
	}

	return append([]byte(nil), content...), nil
}

func (r *Repository) SaveParsedFile(
	ctx context.Context,
	command cardimport_service.ParseFileCommand,
	result cardimport_service.AggregatedFile,
) (cardimport_service.File, error) {
	if result.FileID != command.FileID {
		return cardimport_service.File{}, fmt.Errorf(
			"parsed file ID does not match command: %w",
			core_errors.ErrConflict,
		)
	}

	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return cardimport_service.File{}, fmt.Errorf(
			"begin save parsed cardimport file: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockParsingFile(ctx, tx, command); err != nil {
		return cardimport_service.File{}, err
	}
	if err := clearPreviousParseResult(ctx, tx, command); err != nil {
		return cardimport_service.File{}, err
	}
	if err := copyCards(ctx, tx, command, result.Cards); err != nil {
		return cardimport_service.File{}, err
	}
	if err := copyIssues(ctx, tx, command, result.Issues); err != nil {
		return cardimport_service.File{}, err
	}
	if err := rebuildCrossFileIssues(ctx, tx, command.SessionID); err != nil {
		return cardimport_service.File{}, err
	}

	status := cardimport_service.FileStatusValid
	if result.HasErrors() {
		status = cardimport_service.FileStatusInvalid
	}

	file, err := finishParsingFile(ctx, tx, command, status)
	if err != nil {
		return cardimport_service.File{}, err
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE wb.card_import_sessions
		 SET revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1;`,
		command.SessionID,
	); err != nil {
		return cardimport_service.File{}, fmt.Errorf(
			"update cardimport session revision: %w",
			err,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return cardimport_service.File{}, fmt.Errorf(
			"commit parsed cardimport file: %w",
			err,
		)
	}

	return file, nil
}

func lockParsingFile(
	ctx context.Context,
	tx pgx.Tx,
	command cardimport_service.ParseFileCommand,
) error {
	const query = `
		SELECT file.id
		FROM wb.card_import_files AS file
		JOIN wb.card_import_sessions AS session
			ON session.id = file.session_id
		WHERE file.id = $1
			AND file.session_id = $2
			AND session.author_telegram_id = $3
			AND session.status = 'collecting'
			AND file.status = 'parsing'
		FOR UPDATE OF file, session;
	`

	var fileID int64
	if err := tx.QueryRow(
		ctx,
		query,
		command.FileID,
		command.SessionID,
		command.AuthorTelegramID,
	).Scan(&fileID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf(
				"cardimport file is not in parsing state: %w",
				core_errors.ErrConflict,
			)
		}

		return fmt.Errorf("lock parsing cardimport file: %w", err)
	}

	return nil
}

func clearPreviousParseResult(
	ctx context.Context,
	tx pgx.Tx,
	command cardimport_service.ParseFileCommand,
) error {
	queries := []struct {
		query string
		args  []any
	}{
		{
			query: `DELETE FROM wb.card_import_issues WHERE file_id = $1;`,
			args:  []any{command.FileID},
		},
		{
			query: `DELETE FROM wb.card_import_issues
			        WHERE session_id = $1
			          AND file_id IS NULL
			          AND code IN (
			              'vendor_code_across_files',
			              'barcode_across_files'
			          );`,
			args: []any{command.SessionID},
		},
		{
			query: `DELETE FROM wb.card_import_items WHERE source_file_id = $1;`,
			args:  []any{command.FileID},
		},
	}

	for _, statement := range queries {
		if _, err := tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("clear previous cardimport parse result: %w", err)
		}
	}

	return nil
}

func copyCards(
	ctx context.Context,
	tx pgx.Tx,
	command cardimport_service.ParseFileCommand,
	cards []cardimport_service.AggregatedCard,
) error {
	if len(cards) == 0 {
		return nil
	}

	rows := make([][]any, 0, len(cards))
	for position, card := range cards {
		payload, err := json.Marshal(card)
		if err != nil {
			return fmt.Errorf(
				"marshal cardimport item at position %d: %w",
				position+1,
				err,
			)
		}

		sourceRows := make([]int32, len(card.SourceRows))
		for index, row := range card.SourceRows {
			sourceRows[index] = int32(row)
		}

		rows = append(rows, []any{
			command.SessionID,
			command.FileID,
			position + 1,
			sourceRows,
			card.Group,
			card.Category,
			card.Variant.VendorCode,
			card.Barcodes(),
			string(payload),
		})
	}

	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "card_import_items"},
		[]string{
			"session_id",
			"source_file_id",
			"position",
			"source_rows",
			"group_value",
			"category",
			"vendor_code",
			"barcodes",
			"payload",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy cardimport items: %w", err)
	}

	return nil
}

func copyIssues(
	ctx context.Context,
	tx pgx.Tx,
	command cardimport_service.ParseFileCommand,
	issues []cardimport_service.ParseIssue,
) error {
	if len(issues) == 0 {
		return nil
	}

	rows := make([][]any, 0, len(issues))
	for _, issue := range issues {
		var rowNumber any
		if issue.Row > 0 {
			rowNumber = int32(issue.Row)
		}

		rows = append(rows, []any{
			command.SessionID,
			command.FileID,
			issue.Severity,
			issue.Code,
			issue.SheetName,
			rowNumber,
			issue.Column,
			issue.Message,
		})
	}

	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "card_import_issues"},
		[]string{
			"session_id",
			"file_id",
			"severity",
			"code",
			"sheet_name",
			"row_number",
			"column_name",
			"message",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy cardimport issues: %w", err)
	}

	return nil
}

func rebuildCrossFileIssues(
	ctx context.Context,
	tx pgx.Tx,
	sessionID cardimport_service.SessionID,
) error {
	const vendorCodesQuery = `
		INSERT INTO wb.card_import_issues (
			session_id,
			file_id,
			severity,
			code,
			message
		)
		SELECT
			$1,
			NULL,
			'error',
			'vendor_code_across_files',
			format(
				'Артикул продавца %L встречается в нескольких XLSX-файлах.',
				vendor_code
			)
		FROM wb.card_import_items
		WHERE session_id = $1
		GROUP BY vendor_code
		HAVING COUNT(DISTINCT source_file_id) > 1;
	`
	if _, err := tx.Exec(ctx, vendorCodesQuery, sessionID); err != nil {
		return fmt.Errorf("rebuild cross-file vendor code issues: %w", err)
	}

	const barcodesQuery = `
		INSERT INTO wb.card_import_issues (
			session_id,
			file_id,
			severity,
			code,
			message
		)
		SELECT
			$1,
			NULL,
			'error',
			'barcode_across_files',
			format(
				'Баркод %L встречается в нескольких XLSX-файлах.',
				barcode
			)
		FROM wb.card_import_items AS item
		CROSS JOIN LATERAL unnest(item.barcodes) AS barcode
		WHERE item.session_id = $1
		GROUP BY barcode
		HAVING COUNT(DISTINCT item.source_file_id) > 1;
	`
	if _, err := tx.Exec(ctx, barcodesQuery, sessionID); err != nil {
		return fmt.Errorf("rebuild cross-file barcode issues: %w", err)
	}

	return nil
}

func finishParsingFile(
	ctx context.Context,
	tx pgx.Tx,
	command cardimport_service.ParseFileCommand,
	status cardimport_service.FileStatus,
) (cardimport_service.File, error) {
	query := `
		UPDATE wb.card_import_files
		SET status = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND session_id = $2
			AND status = 'parsing'
		RETURNING ` + fileColumns + `;
	`

	var model fileModel
	if err := model.Scan(tx.QueryRow(
		ctx,
		query,
		command.FileID,
		command.SessionID,
		status,
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.File{}, fmt.Errorf(
				"cardimport parsing state changed: %w",
				core_errors.ErrConflict,
			)
		}

		return cardimport_service.File{}, fmt.Errorf(
			"finish parsing cardimport file: %w",
			err,
		)
	}

	return model.domain(), nil
}
