package wbcabinet_postgres_repository

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
)

type Repository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("wbcabinet PostgreSQL pool is nil")
	}
	return &Repository{pool: pool}
}

func (repository *Repository) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return repository.pool.OperationContext(ctx)
}
