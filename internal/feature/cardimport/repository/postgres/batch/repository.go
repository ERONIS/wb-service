package cardimport_batch_repository

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Batch struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Batch {
	return &Batch{pool: pool}
}
