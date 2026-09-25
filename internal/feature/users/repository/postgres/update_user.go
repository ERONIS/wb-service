package users_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	"github.com/jackc/pgx/v5"
)

func (r *UsersRepository) UpdateUser(
	ctx context.Context,
	user domain.User,
) (domain.User, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		UPDATE wb.users
		SET
			tg_nickname = $1,
			full_name = $2,
			role = $3,
			updated_at = CURRENT_TIMESTAMP
		WHERE tg_id = $4
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
		user.TelegramNickname,
		user.FullName,
		user.Role,
		user.TelegramID,
	)

	var model UserModel

	if err := model.Scan(row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf(
				"user with TelegramID='%d': %w",
				user.TelegramID,
				core_errors.ErrNotFound,
			)
		}

		return domain.User{}, fmt.Errorf(
			"update user with TelegramID='%d': %w",
			user.TelegramID,
			err,
		)
	}

	return modelToDomain(model), nil
}
