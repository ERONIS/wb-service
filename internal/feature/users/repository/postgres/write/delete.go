package users_postgres_write

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (repository *Write) Delete(
	ctx context.Context,
	telegramID int64,
) error {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		DELETE FROM wb.users
		WHERE tg_id = $1;
	`

	commandTag, err := repository.pool.Exec(
		ctx,
		query,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf(
			"delete user with TelegramID='%d': %w",
			telegramID,
			err,
		)
	}

	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf(
			"user with TelegramID='%d': %w",
			telegramID,
			core_errors.ErrNotFound,
		)
	}

	return nil
}
