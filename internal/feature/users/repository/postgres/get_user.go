package users_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	"github.com/jackc/pgx/v5"
)

func (r *UsersRepository) GetUserByTelegramID(
	ctx context.Context,
	telegramID int64,
) (domain.User, error) {
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
		WHERE tg_id = $1;
	`

	row := r.pool.QueryRow(
		ctx,
		query,
		telegramID,
	)

	var model UserModel

	if err := model.Scan(row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf(
				"user with TelegramID='%d': %w",
				telegramID,
				core_errors.ErrNotFound,
			)
		}

		return domain.User{}, fmt.Errorf(
			"scan user with TelegramID='%d': %w",
			telegramID,
			err,
		)
	}

	return modelToDomain(model), nil
}
