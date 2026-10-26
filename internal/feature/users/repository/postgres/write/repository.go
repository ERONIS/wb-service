package users_postgres_write

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Write struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Write {
	return &Write{pool: pool}
}
