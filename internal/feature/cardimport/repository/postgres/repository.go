package cardimport_postgres_repository

import (
	"context"
	"fmt"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
)

type Repository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("cardimport PostgreSQL pool is nil")
	}

	return &Repository{pool: pool}
}

func (r *Repository) HasAuthor(ctx context.Context, authorTelegramID int64) (bool, error) {
	ctx, cancel := r.pool.OperationContext(ctx)
	defer cancel()
	var exists bool
	if err := r.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM wb.users WHERE tg_id = $1)", authorTelegramID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check cardimport author: %w", err)
	}
	return exists, nil
}
