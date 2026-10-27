package users_postgres_read

import (
	"context"
	"fmt"
	users_postgres_row "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"

	"github.com/jackc/pgx/v5"
)

// List возвращает срез пользователей с пагинацией по имени и идентификатору.
func (repository *Read) List(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.User, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
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

	rows, err := repository.pool.Query(
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

	dtos, err := pgx.CollectRows(rows, pgx.RowToStructByName[users_postgres_row.Row])
	if err != nil {
		return nil, fmt.Errorf(
			"collect users: %w",
			err,
		)
	}

	return users_postgres_row.ToDomains(dtos), nil
}
