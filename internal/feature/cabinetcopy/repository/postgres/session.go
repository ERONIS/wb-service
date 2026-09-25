package cabinetcopy_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

const sessionColumns = `
	session.id,
	session.author_telegram_id,
	session.step,
	session.revision,
	COALESCE(session.source_cabinet_id, ''),
	COALESCE(session.target_cabinet_id, ''),
	COALESCE(session.requested_count, 0),
	session.selected_tag_ids,
	session.selected_tag_names,
	session.prepared_count,
	COALESCE(session.batch_id, 0),
	COALESCE(session.transfer_id, 0),
	session.created_at,
	session.updated_at,
	session.submitted_at
`

type rowScanner interface {
	Scan(...any) error
}

func scanSession(row rowScanner) (cabinetcopy_service.Session, error) {
	var session cabinetcopy_service.Session
	var source, target string
	var batchID, transferID int64
	if err := row.Scan(
		&session.ID,
		&session.AuthorTelegramID,
		&session.Step,
		&session.Revision,
		&source,
		&target,
		&session.RequestedCount,
		&session.SelectedTagIDs,
		&session.SelectedTagNames,
		&session.PreparedCount,
		&batchID,
		&transferID,
		&session.CreatedAt,
		&session.UpdatedAt,
		&session.SubmittedAt,
	); err != nil {
		return cabinetcopy_service.Session{}, err
	}
	session.SourceCabinetID = cabinetcopy_service.CabinetID(source)
	session.TargetCabinetID = cabinetcopy_service.CabinetID(target)
	session.BatchID = cardimport_service.BatchID(batchID)
	session.TransferID = transfer_service.TransferID(transferID)
	if err := session.Validate(); err != nil {
		return cabinetcopy_service.Session{}, err
	}
	return session, nil
}

func (repository *Repository) GetOrCreate(
	ctx context.Context,
	authorTelegramID int64,
) (cabinetcopy_service.Session, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const insert = `
		INSERT INTO wb.cabinet_copy_sessions AS session (author_user_id, author_telegram_id)
		VALUES ((SELECT id FROM wb.users WHERE tg_id = $1), $1)
		ON CONFLICT (author_telegram_id)
			WHERE step NOT IN ('submitted', 'cancelled')
		DO NOTHING
		RETURNING ` + sessionColumns + `;
	`
	session, err := scanSession(repository.pool.QueryRow(ctx, insert, authorTelegramID))
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return cabinetcopy_service.Session{}, fmt.Errorf("insert cabinet copy session: %w", err)
	}
	return repository.getActive(ctx, repository.pool, authorTelegramID)
}

func (repository *Repository) Get(
	ctx context.Context,
	authorTelegramID int64,
	sessionID cabinetcopy_service.SessionID,
) (cabinetcopy_service.Session, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return repository.get(ctx, repository.pool, authorTelegramID, sessionID)
}

func (repository *Repository) GetActive(
	ctx context.Context,
	authorTelegramID int64,
) (cabinetcopy_service.Session, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return repository.getActive(ctx, repository.pool, authorTelegramID)
}

func (repository *Repository) get(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	authorTelegramID int64,
	sessionID cabinetcopy_service.SessionID,
) (cabinetcopy_service.Session, error) {
	query := `SELECT ` + sessionColumns + ` FROM wb.cabinet_copy_sessions AS session
		WHERE session.id = $1 AND session.author_telegram_id = $2;`
	session, err := scanSession(db.QueryRow(ctx, query, sessionID, authorTelegramID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cabinetcopy_service.Session{}, fmt.Errorf("cabinet copy session: %w", core_errors.ErrNotFound)
	}
	if err != nil {
		return cabinetcopy_service.Session{}, fmt.Errorf("scan cabinet copy session: %w", err)
	}
	return session, nil
}

func (repository *Repository) getActive(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	authorTelegramID int64,
) (cabinetcopy_service.Session, error) {
	query := `SELECT ` + sessionColumns + ` FROM wb.cabinet_copy_sessions AS session
		WHERE session.author_telegram_id = $1
		  AND session.step NOT IN ('submitted', 'cancelled')
		ORDER BY session.id DESC LIMIT 1;`
	session, err := scanSession(db.QueryRow(ctx, query, authorTelegramID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cabinetcopy_service.Session{}, fmt.Errorf("active cabinet copy session: %w", core_errors.ErrNotFound)
	}
	if err != nil {
		return cabinetcopy_service.Session{}, fmt.Errorf("scan active cabinet copy session: %w", err)
	}
	return session, nil
}

func (repository *Repository) SetSource(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64, cabinet cabinetcopy_service.CabinetID) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET source_cabinet_id = $4, step = 'target', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'source'
		RETURNING `+sessionColumns+`;`, id, author, revision, cabinet)
}

func (repository *Repository) SetTarget(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64, cabinet cabinetcopy_service.CabinetID) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET target_cabinet_id = $4, step = 'count', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'target'
		  AND source_cabinet_id <> $4
		RETURNING `+sessionColumns+`;`, id, author, revision, cabinet)
}

func (repository *Repository) RequestCountInput(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET step = 'count_input', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'count'
		RETURNING `+sessionColumns+`;`, id, author, revision)
}

func (repository *Repository) SetCount(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64, count int) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET requested_count = $4, step = 'tags', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3
		  AND step IN ('count', 'count_input')
		RETURNING `+sessionColumns+`;`, id, author, revision, count)
}

func (repository *Repository) SetTags(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64, tags []cabinetcopy_service.Tag) (cabinetcopy_service.Session, error) {
	ids := make([]int64, len(tags))
	names := make([]string, len(tags))
	for index := range tags {
		ids[index] = tags[index].ID
		names[index] = tags[index].Name
	}
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET selected_tag_ids = $4, selected_tag_names = $5,
			revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'tags'
		RETURNING `+sessionColumns+`;`, id, author, revision, ids, names)
}

func (repository *Repository) update(ctx context.Context, query string, arguments ...any) (cabinetcopy_service.Session, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	session, err := scanSession(repository.pool.QueryRow(ctx, query, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return cabinetcopy_service.Session{}, fmt.Errorf("cabinet copy session changed: %w", core_errors.ErrConflict)
	}
	if err != nil {
		return cabinetcopy_service.Session{}, fmt.Errorf("update cabinet copy session: %w", err)
	}
	return session, nil
}

func (repository *Repository) SavePrepared(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	author int64,
	id cabinetcopy_service.SessionID,
	revision int64,
	items []cabinetcopy_service.PreparedItem,
) (cabinetcopy_service.Session, error) {
	if tx == nil || len(items) == 0 {
		return cabinetcopy_service.Session{}, core_errors.ErrInvalidArgument
	}
	query := `UPDATE wb.cabinet_copy_sessions AS session
		SET step = 'review', prepared_count = $4, revision = revision + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'tags'
		RETURNING ` + sessionColumns + `;`
	session, err := scanSession(tx.QueryRow(ctx, query, id, author, revision, len(items)))
	if errors.Is(err, pgx.ErrNoRows) {
		return cabinetcopy_service.Session{}, core_errors.ErrConflict
	}
	if err != nil {
		return cabinetcopy_service.Session{}, fmt.Errorf("mark cabinet copy prepared: %w", err)
	}
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "cabinet_copy_items"},
		[]string{"session_id", "position", "source_nm_id", "source_imt_id", "vendor_code", "payload"},
		pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			item := items[index]
			if err := item.Validate(); err != nil {
				return nil, err
			}
			payload, encodeErr := json.Marshal(item.Card)
			if encodeErr != nil {
				return nil, encodeErr
			}
			return []any{id, item.Position, item.NMID, item.IMTID, item.Card.Variant.VendorCode, string(payload)}, nil
		}),
	)
	if err != nil {
		return cabinetcopy_service.Session{}, fmt.Errorf("copy prepared cabinet copy items: %w", err)
	}
	if count != int64(len(items)) {
		return cabinetcopy_service.Session{}, core_errors.ErrConflict
	}
	return session, nil
}

func (repository *Repository) ListPrepared(ctx context.Context, author int64, id cabinetcopy_service.SessionID) ([]cabinetcopy_service.PreparedItem, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT item.position, item.source_nm_id, item.source_imt_id, item.payload
		FROM wb.cabinet_copy_items AS item
		JOIN wb.cabinet_copy_sessions AS session ON session.id = item.session_id
		WHERE item.session_id = $1 AND session.author_telegram_id = $2
		ORDER BY item.position;
	`
	rows, err := repository.pool.Query(ctx, query, id, author)
	if err != nil {
		return nil, fmt.Errorf("select prepared cabinet copy items: %w", err)
	}
	defer rows.Close()
	items := make([]cabinetcopy_service.PreparedItem, 0)
	for rows.Next() {
		var item cabinetcopy_service.PreparedItem
		var payload []byte
		if err := rows.Scan(&item.Position, &item.NMID, &item.IMTID, &payload); err != nil {
			return nil, fmt.Errorf("scan prepared cabinet copy item: %w", err)
		}
		if err := json.Unmarshal(payload, &item.Card); err != nil {
			return nil, fmt.Errorf("decode prepared cabinet copy item: %w", err)
		}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prepared cabinet copy items: %w", err)
	}
	return items, nil
}

func (repository *Repository) AttachBatch(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64, batchID cardimport_service.BatchID) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET batch_id = $4, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3 AND step = 'review'
		  AND (batch_id IS NULL OR batch_id = $4)
		RETURNING `+sessionColumns+`;`, id, author, revision, batchID)
}

func (repository *Repository) MarkSubmitted(ctx context.Context, author int64, id cabinetcopy_service.SessionID, batchID cardimport_service.BatchID, transferID transfer_service.TransferID) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET batch_id = $3, transfer_id = $4, step = 'submitted',
			revision = revision + 1, submitted_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND step = 'review'
		  AND (batch_id IS NULL OR batch_id = $3)
		RETURNING `+sessionColumns+`;`, id, author, batchID, transferID)
}

func (repository *Repository) Cancel(ctx context.Context, author int64, id cabinetcopy_service.SessionID, revision int64) (cabinetcopy_service.Session, error) {
	return repository.update(ctx, `
		UPDATE wb.cabinet_copy_sessions AS session
		SET step = 'cancelled', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND author_telegram_id = $2 AND revision = $3
		  AND step NOT IN ('submitted', 'cancelled')
		  AND batch_id IS NULL AND transfer_id IS NULL
		RETURNING `+sessionColumns+`;`, id, author, revision)
}

var _ cabinetcopy_service.Repository = (*Repository)(nil)
