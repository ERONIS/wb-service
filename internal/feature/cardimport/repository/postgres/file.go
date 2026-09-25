package cardimport_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

const fileColumns = `
	id,
	session_id,
	telegram_file_id,
	telegram_file_unique_id,
	telegram_message_id,
	original_filename,
	mime_type,
	declared_size,
	stored_size,
	sha256,
	status,
	created_at,
	updated_at
`

func (r *Repository) ListRecoverableFiles(
	ctx context.Context,
	staleParsingBefore time.Time,
	reparsePriceErrorsBefore time.Time,
	limit int,
) ([]cardimport_service.RecoverableFile, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT
			session.author_telegram_id,
			file.id,
			file.session_id,
			file.telegram_file_id,
			file.telegram_file_unique_id,
			file.telegram_message_id,
			file.original_filename,
			file.mime_type,
			file.declared_size,
			file.stored_size,
			file.sha256,
			file.status,
			file.created_at,
			file.updated_at
		FROM wb.card_import_files AS file
		JOIN wb.card_import_sessions AS session
			ON session.id = file.session_id
		WHERE session.status = 'collecting'
			AND (
				file.status IN ('reserved', 'stored')
				OR (
					file.status = 'parsing'
					AND file.updated_at <= $1
				)
				OR (
					file.status = 'invalid'
					AND file.updated_at < $2
					AND EXISTS (
						SELECT 1
						FROM wb.card_import_issues AS issue
						WHERE issue.file_id = file.id
							AND issue.code = 'price_invalid'
					)
				)
			)
		ORDER BY file.updated_at, file.id
		LIMIT $3;
	`

	rows, err := r.pool.Query(
		ctx,
		query,
		staleParsingBefore,
		reparsePriceErrorsBefore,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recoverable cardimport files: %w", err)
	}
	defer rows.Close()

	files := make([]cardimport_service.RecoverableFile, 0)
	for rows.Next() {
		var ownerID int64
		var model fileModel
		if err := rows.Scan(
			&ownerID,
			&model.ID,
			&model.SessionID,
			&model.TelegramFileID,
			&model.TelegramFileUniqueID,
			&model.TelegramMessageID,
			&model.OriginalFilename,
			&model.MIMEType,
			&model.DeclaredSize,
			&model.StoredSize,
			&model.SHA256,
			&model.Status,
			&model.CreatedAt,
			&model.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recoverable cardimport file: %w", err)
		}
		files = append(files, cardimport_service.RecoverableFile{
			AuthorTelegramID: ownerID,
			File:             model.domain(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable cardimport files: %w", err)
	}

	return files, nil
}

func (r *Repository) ReserveFile(
	ctx context.Context,
	command cardimport_service.ReserveFileCommand,
	maxFiles int,
) (cardimport_service.File, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		WITH locked_session AS MATERIALIZED (
			SELECT id
			FROM wb.card_import_sessions
			WHERE id = $1
				AND author_telegram_id = $2
				AND status = 'collecting'
			FOR UPDATE
		),
		existing AS (
			SELECT
				file.id,
				file.session_id,
				file.telegram_file_id,
				file.telegram_file_unique_id,
				file.telegram_message_id,
				file.original_filename,
				file.mime_type,
				file.declared_size,
				file.stored_size,
				file.sha256,
				file.status,
				file.created_at,
				file.updated_at
			FROM wb.card_import_files AS file
			JOIN locked_session AS session
				ON session.id = file.session_id
			WHERE file.telegram_file_id = $3
				OR file.telegram_file_unique_id = $4
				OR file.telegram_message_id = $5
			ORDER BY file.id
			LIMIT 1
		),
		inserted AS (
			INSERT INTO wb.card_import_files (
				session_id,
				telegram_file_id,
				telegram_file_unique_id,
				telegram_message_id,
				original_filename,
				mime_type,
				declared_size
			)
			SELECT
				session.id,
				$3,
				$4,
				$5,
				$6,
				$7,
				$8
			FROM locked_session AS session
			WHERE NOT EXISTS (SELECT 1 FROM existing)
				AND (
					$9 <= 0
					OR (
						SELECT COUNT(*)
						FROM wb.card_import_files AS file_count
						WHERE file_count.session_id = session.id
							AND file_count.status <> 'abandoned'
					) < $9
				)
			ON CONFLICT DO NOTHING
			RETURNING
				id,
				session_id,
				telegram_file_id,
				telegram_file_unique_id,
				telegram_message_id,
				original_filename,
				mime_type,
				declared_size,
				stored_size,
				sha256,
				status,
				created_at,
				updated_at
		)
		SELECT * FROM existing
		UNION ALL
		SELECT * FROM inserted
		LIMIT 1;
	`

	var model fileModel
	if err := model.Scan(r.pool.QueryRow(
		ctx,
		query,
		command.SessionID,
		command.AuthorTelegramID,
		command.TelegramFileID,
		command.TelegramFileUniqueID,
		command.TelegramMessageID,
		command.OriginalFilename,
		command.MIMEType,
		command.DeclaredSize,
		maxFiles,
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.File{}, fmt.Errorf(
				"cardimport session is unavailable or file limit is reached: %w",
				core_errors.ErrConflict,
			)
		}

		return cardimport_service.File{}, fmt.Errorf(
			"scan reserved cardimport file: %w",
			err,
		)
	}

	return model.domain(), nil
}

func (r *Repository) StoreFile(
	ctx context.Context,
	command cardimport_service.StoreFileCommand,
	content cardimport_service.StoredContent,
) (cardimport_service.File, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
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
		target AS MATERIALIZED (
			SELECT file.id
			FROM wb.card_import_files AS file
			JOIN locked_session AS session
				ON session.id = file.session_id
			WHERE file.id = $1
				AND file.session_id = $2
				AND file.status IN ('reserved', 'stored')
			FOR UPDATE OF file
		),
		stored AS (
			UPDATE wb.card_import_files AS file
			SET
				stored_size = $5,
				sha256 = $6,
				content = $4,
				status = 'stored',
				updated_at = CURRENT_TIMESTAMP
			FROM target
			WHERE file.id = target.id
				AND (
					file.status = 'reserved'
					OR (
						file.stored_size = $5
						AND file.sha256 = $6
						AND file.content = $4
					)
				)
			RETURNING
				file.id,
				file.session_id,
				file.telegram_file_id,
				file.telegram_file_unique_id,
				file.telegram_message_id,
				file.original_filename,
				file.mime_type,
				file.declared_size,
				file.stored_size,
				file.sha256,
				file.status,
				file.created_at,
				file.updated_at
		)
		SELECT * FROM stored;
	`

	var model fileModel
	if err := model.Scan(r.pool.QueryRow(
		ctx,
		query,
		command.FileID,
		command.SessionID,
		command.AuthorTelegramID,
		content.Bytes,
		len(content.Bytes),
		content.SHA256[:],
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.File{}, fmt.Errorf(
				"cardimport file is unavailable or content differs from stored file: %w",
				core_errors.ErrConflict,
			)
		}

		return cardimport_service.File{}, fmt.Errorf(
			"scan stored cardimport file: %w",
			err,
		)
	}

	return model.domain(), nil
}
