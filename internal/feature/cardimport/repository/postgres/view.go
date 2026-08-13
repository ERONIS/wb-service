package cardimport_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) GetCollectingSessionView(
	ctx context.Context,
	authorTelegramID int64,
) (cardimport_service.SessionView, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	const query = `
		SELECT ` + sessionColumns + `
		FROM wb.card_import_sessions
		WHERE author_telegram_id = $1
			AND status = 'collecting';
	`

	var model sessionModel
	if err := model.Scan(r.pool.QueryRow(ctx, query, authorTelegramID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"collecting cardimport session: %w",
				core_errors.ErrNotFound,
			)
		}

		return cardimport_service.SessionView{}, fmt.Errorf(
			"scan collecting cardimport session: %w",
			err,
		)
	}

	return r.loadSessionView(ctx, model.domain())
}

func (r *Repository) GetSessionView(
	ctx context.Context,
	authorTelegramID int64,
	sessionID cardimport_service.SessionID,
) (cardimport_service.SessionView, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pool.OpTimeout())
	defer cancel()

	session, err := r.getSession(ctx, authorTelegramID, sessionID)
	if err != nil {
		return cardimport_service.SessionView{}, err
	}

	return r.loadSessionView(ctx, session)
}

func (r *Repository) loadSessionView(
	ctx context.Context,
	session cardimport_service.Session,
) (cardimport_service.SessionView, error) {
	const filesQuery = `
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
			file.updated_at,
			(
				SELECT COUNT(*)
				FROM wb.card_import_items AS item
				WHERE item.source_file_id = file.id
			),
			(
				SELECT COUNT(*)
				FROM wb.card_import_issues AS issue
				WHERE issue.file_id = file.id
					AND issue.severity = 'error'
			)
		FROM wb.card_import_files AS file
		WHERE file.session_id = $1
			AND file.status <> 'abandoned'
		ORDER BY file.id;
	`

	rows, err := r.pool.Query(ctx, filesQuery, session.ID)
	if err != nil {
		return cardimport_service.SessionView{}, fmt.Errorf(
			"query cardimport session files: %w",
			err,
		)
	}
	defer rows.Close()

	view := cardimport_service.SessionView{
		Session: session,
		Files:   make([]cardimport_service.FileView, 0),
		Issues:  make([]cardimport_service.IssueView, 0),
	}
	allFilesValid := true

	for rows.Next() {
		var model fileModel
		var cardsCount int
		var errorsCount int
		if err := rows.Scan(
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
			&cardsCount,
			&errorsCount,
		); err != nil {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"scan cardimport session file view: %w",
				err,
			)
		}

		view.Files = append(view.Files, cardimport_service.FileView{
			File:        model.domain(),
			CardsCount:  cardsCount,
			ErrorsCount: errorsCount,
		})
		view.CardsCount += cardsCount
		if model.Status != cardimport_service.FileStatusValid {
			allFilesValid = false
		}
	}
	if err := rows.Err(); err != nil {
		return cardimport_service.SessionView{}, fmt.Errorf(
			"iterate cardimport session file views: %w",
			err,
		)
	}
	rows.Close()

	const errorsQuery = `
		SELECT COUNT(*)
		FROM wb.card_import_issues
		WHERE session_id = $1
			AND severity = 'error';
	`
	if err := r.pool.QueryRow(
		ctx,
		errorsQuery,
		session.ID,
	).Scan(&view.ErrorsCount); err != nil {
		return cardimport_service.SessionView{}, fmt.Errorf(
			"count cardimport session errors: %w",
			err,
		)
	}

	const issuesQuery = `
		SELECT
			issue.file_id,
			COALESCE(file.original_filename, ''),
			issue.severity,
			issue.code,
			issue.message,
			issue.sheet_name,
			issue.row_number,
			issue.column_name
		FROM wb.card_import_issues AS issue
		LEFT JOIN wb.card_import_files AS file
			ON file.id = issue.file_id
		WHERE issue.session_id = $1
		ORDER BY
			CASE issue.severity WHEN 'error' THEN 0 ELSE 1 END,
			issue.id
		LIMIT 20;
	`
	issueRows, err := r.pool.Query(ctx, issuesQuery, session.ID)
	if err != nil {
		return cardimport_service.SessionView{}, fmt.Errorf(
			"query cardimport session issues: %w",
			err,
		)
	}
	defer issueRows.Close()

	for issueRows.Next() {
		var fileID *int64
		var rowNumber *int32
		var issue cardimport_service.IssueView
		if err := issueRows.Scan(
			&fileID,
			&issue.Filename,
			&issue.Issue.Severity,
			&issue.Issue.Code,
			&issue.Issue.Message,
			&issue.Issue.SheetName,
			&rowNumber,
			&issue.Issue.Column,
		); err != nil {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"scan cardimport session issue: %w",
				err,
			)
		}
		if fileID != nil {
			issue.FileID = cardimport_service.FileID(*fileID)
		}
		if rowNumber != nil {
			issue.Issue.Row = int(*rowNumber)
		}
		view.Issues = append(view.Issues, issue)
	}
	if err := issueRows.Err(); err != nil {
		return cardimport_service.SessionView{}, fmt.Errorf(
			"iterate cardimport session issues: %w",
			err,
		)
	}

	view.Ready = session.Status == cardimport_service.SessionStatusCollecting &&
		len(view.Files) > 0 &&
		allFilesValid &&
		view.ErrorsCount == 0

	return view, nil
}
