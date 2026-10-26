package users_postgres_write

import (
	"context"
	"errors"
	"fmt"
	users_postgres_row "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	"github.com/jackc/pgx/v5"
)

// Update обновляет данные пользователя в wb.users по его tg_id.
func (repository *Write) Update(
	ctx context.Context,
	user domain.User,
) (domain.User, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
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

	row := repository.pool.QueryRow(
		ctx,
		query,
		user.TelegramNickname,
		user.FullName,
		user.Role,
		user.TelegramID,
	)

	var dto users_postgres_row.Row

	if err := dto.Scan(row); err != nil {
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

	return users_postgres_row.ToDomain(dto), nil
}
