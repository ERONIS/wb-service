package users_postgres_write

import (
	"context"
	"fmt"
	users_postgres_row "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/internal/row"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	"github.com/ERONIS/wb-service/internal/core/repository/postgres/pgerr"
)

// Create сохраняет нового пользователя в wb.users и возвращает созданную сущность.
func (repository *Write) Create(
	ctx context.Context,
	user domain.User,
) (domain.User, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
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

	row := repository.pool.QueryRow(
		ctx,
		query,
		user.TelegramID,
		user.TelegramNickname,
		user.FullName,
		user.Role,
	)

	var dto users_postgres_row.Row

	if err := dto.Scan(row); err != nil {
		if pgerr.IsUniqueViolation(err) {
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

	return users_postgres_row.ToDomain(dto), nil
}
