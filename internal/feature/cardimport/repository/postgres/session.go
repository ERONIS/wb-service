package cardimport_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

const sessionColumns = `
	id,
	author_telegram_id,
	purpose,
	status,
	revision,
	created_at,
	updated_at
`

func (r *Repository) GetOrCreateCollectingSession(
	ctx context.Context,
	authorTelegramID int64,
	purpose cardimport_service.Purpose,
) (cardimport_service.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	const query = `
		INSERT INTO wb.card_import_sessions (
			author_user_id,
			author_telegram_id,
			purpose
		)
		SELECT id, tg_id, $2
		FROM wb.users
		WHERE tg_id = $1
		ON CONFLICT (author_telegram_id)
			WHERE status = 'collecting'
		DO UPDATE SET
			author_telegram_id = EXCLUDED.author_telegram_id
		RETURNING
			id,
			author_telegram_id,
			purpose,
			status,
			revision,
			created_at,
			updated_at;
	`

	var model sessionModel
	if err := model.Scan(r.pool.QueryRow(
		ctx,
		query,
		authorTelegramID,
		purpose,
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.Session{}, fmt.Errorf(
				"user with TelegramID='%d': %w",
				authorTelegramID,
				core_errors.ErrNotFound,
			)
		}

		return cardimport_service.Session{}, fmt.Errorf(
			"scan collecting cardimport session: %w",
			err,
		)
	}

	return model.domain(), nil
}

func (r *Repository) GetSession(
	ctx context.Context,
	authorTelegramID int64,
	sessionID cardimport_service.SessionID,
) (cardimport_service.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	return r.getSession(ctx, authorTelegramID, sessionID)
}

func (r *Repository) getSession(
	ctx context.Context,
	authorTelegramID int64,
	sessionID cardimport_service.SessionID,
) (cardimport_service.Session, error) {
	query := `
		SELECT ` + sessionColumns + `
		FROM wb.card_import_sessions
		WHERE id = $1
			AND author_telegram_id = $2;
	`

	var model sessionModel
	if err := model.Scan(r.pool.QueryRow(
		ctx,
		query,
		sessionID,
		authorTelegramID,
	)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.Session{}, fmt.Errorf(
				"cardimport session ID='%d': %w",
				sessionID,
				core_errors.ErrNotFound,
			)
		}

		return cardimport_service.Session{}, fmt.Errorf(
			"scan cardimport session ID='%d': %w",
			sessionID,
			err,
		)
	}

	return model.domain(), nil
}

func (r *Repository) CancelSession(
	ctx context.Context,
	authorTelegramID int64,
	sessionID cardimport_service.SessionID,
) (cardimport_service.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	const query = `
		WITH cancelled AS (
			UPDATE wb.card_import_sessions
			SET
				status = 'cancelled',
				revision = CASE
					WHEN status = 'collecting' THEN revision + 1
					ELSE revision
				END,
				updated_at = CASE
					WHEN status = 'collecting' THEN CURRENT_TIMESTAMP
					ELSE updated_at
				END
			WHERE id = $1
				AND author_telegram_id = $2
				AND status IN ('collecting', 'cancelled')
			RETURNING
				id,
				author_telegram_id,
				purpose,
				status,
				revision,
				created_at,
				updated_at
		),
		abandoned AS (
			UPDATE wb.card_import_files AS file
			SET
				status = 'abandoned',
				updated_at = CURRENT_TIMESTAMP
			FROM cancelled
			WHERE file.session_id = cancelled.id
				AND file.status IN (
					'reserved',
					'stored',
					'parsing',
					'valid',
					'invalid'
				)
		)
		SELECT
			id,
			author_telegram_id,
			purpose,
			status,
			revision,
			created_at,
			updated_at
		FROM cancelled;
	`

	var model sessionModel
	if err := model.Scan(r.pool.QueryRow(
		ctx,
		query,
		sessionID,
		authorTelegramID,
	)); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.Session{}, fmt.Errorf(
				"scan cancelled cardimport session: %w",
				err,
			)
		}

		session, getErr := r.getSession(
			ctx,
			authorTelegramID,
			sessionID,
		)
		if getErr != nil {
			return cardimport_service.Session{}, getErr
		}

		return cardimport_service.Session{}, fmt.Errorf(
			"cannot cancel cardimport session with status '%s': %w",
			session.Status,
			core_errors.ErrConflict,
		)
	}

	return model.domain(), nil
}
