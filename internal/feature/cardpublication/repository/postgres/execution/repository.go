package execution

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type ExecutionRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *ExecutionRepository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &ExecutionRepository{pool: pool}
}
