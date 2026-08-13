package cardimport_postgres_repository

import (
	"context"
	"errors"
	"fmt"

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

func (r *Repository) ReserveFile(
	ctx context.Context,
	command cardimport_service.ReserveFileCommand,
	maxFiles int,
) (cardimport_service.File, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
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
					SELECT COUNT(*)
					FROM wb.card_import_files AS file_count
					WHERE file_count.session_id = session.id
						AND file_count.status <> 'abandoned'
				) < $9
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
		persisted_blob AS (
			INSERT INTO wb.card_import_file_blobs (
				file_id,
				content,
				size,
				sha256
			)
			SELECT id, $4, $5, $6
			FROM target
			ON CONFLICT (file_id) DO UPDATE SET
				content = wb.card_import_file_blobs.content
			WHERE wb.card_import_file_blobs.size = EXCLUDED.size
				AND wb.card_import_file_blobs.sha256 = EXCLUDED.sha256
			RETURNING file_id
		),
		stored AS (
			UPDATE wb.card_import_files AS file
			SET
				stored_size = $5,
				sha256 = $6,
				status = 'stored',
				updated_at = CURRENT_TIMESTAMP
			FROM persisted_blob
			WHERE file.id = persisted_blob.file_id
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
				"cardimport file is unavailable or content differs from stored blob: %w",
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
