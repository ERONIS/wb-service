package users_postgres_read

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Read struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Read {
	return &Read{pool: pool}
}
