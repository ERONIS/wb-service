package cardimport_author_repository

import (
	"context"
	"fmt"
)

func (repository *Author) HasAuthor(ctx context.Context, authorTelegramID int64) (bool, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	var exists bool
	if err := repository.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM wb.users WHERE tg_id = $1)", authorTelegramID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check cardimport author: %w", err)
	}
	return exists, nil
}
