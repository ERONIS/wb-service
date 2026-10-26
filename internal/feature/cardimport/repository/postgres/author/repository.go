package cardimport_author_repository

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Author struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Author {
	return &Author{pool: pool}
}
