package users_postgres_repository

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

func (r *UsersRepository) GetUsers(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.User, error) {
	ctx, cancel := context.WithTimeout(
		ctx,
		r.pool.OpTimeout(),
	)
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
	defer rows.Close()

	var models []UserModel

	for rows.Next() {
		var model UserModel

		if err := model.Scan(rows); err != nil {
			return nil, fmt.Errorf(
				"scan user: %w",
				err,
			)
		}

		models = append(models, model)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate users: %w",
			err,
		)
	}

	return modelsToDomains(models), nil
}
