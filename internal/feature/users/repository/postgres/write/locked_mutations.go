package users_postgres_write

import (
	"context"
	"errors"
	"fmt"
	users_postgres_row "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"

	"github.com/jackc/pgx/v5"
)

// LockUsersForMutation блокирует участников операции в порядке Telegram ID.
func (repository *Write) LockUsersForMutation(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	adminTelegramID int64,
	targetTelegramID int64,
) (map[int64]domain.User, error) {
	const query = `
		SELECT id, tg_id, tg_nickname, full_name, role, created_at, updated_at
		FROM wb.users
		WHERE tg_id = $1 OR tg_id = $2
		ORDER BY tg_id
		FOR UPDATE;
	`

	rows, err := db.Query(ctx, query, adminTelegramID, targetTelegramID)
	if err != nil {
		return nil, fmt.Errorf("lock users for mutation: %w", err)
	}
	defer rows.Close()

	users := make(map[int64]domain.User, 2)
	for rows.Next() {
		var dto users_postgres_row.Row
		if err := dto.Scan(rows); err != nil {
			return nil, fmt.Errorf("scan locked user: %w", err)
		}
		users[dto.TelegramID] = users_postgres_row.ToDomain(dto)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read locked users: %w", err)
	}
	return users, nil
}

// UpdateRoleWithin меняет только роль пользователя в переданной транзакции.
func (repository *Write) UpdateRoleWithin(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	telegramID int64,
	role domain.UserRole,
) (domain.User, error) {
	const query = `
		UPDATE wb.users
		SET role = $1, updated_at = CURRENT_TIMESTAMP
		WHERE tg_id = $2
		RETURNING id, tg_id, tg_nickname, full_name, role, created_at, updated_at;
	`

	var dto users_postgres_row.Row
	if err := dto.Scan(db.QueryRow(ctx, query, role, telegramID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf("user with TelegramID='%d': %w", telegramID, core_errors.ErrNotFound)
		}
		return domain.User{}, fmt.Errorf("update user role with TelegramID='%d': %w", telegramID, err)
	}
	return users_postgres_row.ToDomain(dto), nil
}

// DeleteWithin удаляет пользователя в переданной транзакции.
func (repository *Write) DeleteWithin(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	telegramID int64,
) error {
	const query = `DELETE FROM wb.users WHERE tg_id = $1;`

	tag, err := db.Exec(ctx, query, telegramID)
	if err != nil {
		return fmt.Errorf("delete user with TelegramID='%d': %w", telegramID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user with TelegramID='%d': %w", telegramID, core_errors.ErrNotFound)
	}
	return nil
}
