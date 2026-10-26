package users_postgres_read

import (
	"context"
	"errors"
	"fmt"
	users_postgres_row "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	"github.com/jackc/pgx/v5"
)

// FindByTelegramID находит пользователя по его TelegramID.
func (repository *Read) FindByTelegramID(
	ctx context.Context,
	telegramID int64,
) (domain.User, error) {
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
		WHERE tg_id = $1;
	`

	row := repository.pool.QueryRow(
		ctx,
		query,
		telegramID,
	)

	var dto users_postgres_row.Row

	if err := dto.Scan(row); err != nil {
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

	return users_postgres_row.ToDomain(dto), nil
}
