package wbcabinet_postgres_repository

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	wbcabinet_postgres_read "github.com/ERONIS/wb-service/internal/feature/wbcabinet/repository/postgres/read"
	wbcabinet_postgres_write "github.com/ERONIS/wb-service/internal/feature/wbcabinet/repository/postgres/write"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

type Repository struct {
	*wbcabinet_postgres_read.Read
	*wbcabinet_postgres_write.Write
	pool core_postgres_pool.Pool
}

var _ wbcabinet_service.Repository = (*Repository)(nil)

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("wbcabinet PostgreSQL pool is nil")
	}
	return &Repository{
		Read:  wbcabinet_postgres_read.New(pool),
		Write: wbcabinet_postgres_write.New(pool),
		pool:  pool,
	}
}

func (repository *Repository) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return repository.pool.OperationContext(ctx)
}
