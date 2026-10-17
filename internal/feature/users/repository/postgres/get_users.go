package users_postgres_repository

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"

	"github.com/jackc/pgx/v5"
)

// GetUsers возвращает срез пользователей с пагинацией по имени и идентификатору.
func (r *UsersRepository) GetUsers(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.User, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT
			id,
			tg_id,
			tg_nickname,
			full_name,
			role,
			created_at,
			updated_at
		FROM wb.users
		ORDER BY full_name, id
		LIMIT $1
		OFFSET $2;
	`

	rows, err := r.pool.Query(
		ctx,
		query,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"select users: %w",
			err,
		)
	}

	dtos, err := pgx.CollectRows(rows, pgx.RowToStructByName[userRowDTO])
	if err != nil {
		return nil, fmt.Errorf(
			"collect users: %w",
			err,
		)
	}

	return dtosToDomains(dtos), nil
}
