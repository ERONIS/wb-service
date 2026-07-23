package users_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const postgresUniqueViolationCode = "23505"

func (r *UsersRepository) SaveUser(
	ctx context.Context,
	user domain.User,
) (domain.User, error) {
	ctx, cancel := context.WithTimeout(
		ctx,
		r.pool.OpTimeout(),
	)
	defer cancel()

	const query = `
		INSERT INTO wb.users (
			tg_id,
			tg_nickname,
			full_name,
			role
		)
		VALUES ($1, $2, $3, $4)
		RETURNING
			id,
			tg_id,
			tg_nickname,
			full_name,
			role,
			created_at,
			updated_at;
	`

	row := r.pool.QueryRow(
		ctx,
		query,
		user.TelegramID,
		user.TelegramNickname,
		user.FullName,
		user.Role,
	)

	var model UserModel

	if err := model.Scan(row); err != nil {
		var postgresError *pgconn.PgError

		if errors.As(err, &postgresError) &&
			postgresError.Code == postgresUniqueViolationCode {
			return domain.User{}, fmt.Errorf(
				"user with TelegramID='%d' already exists: %w",
				user.TelegramID,
				core_errors.ErrConflict,
			)
		}

		return domain.User{}, fmt.Errorf(
			"scan saved user: %w",
			err,
		)
	}

	return modelToDomain(model), nil
}
