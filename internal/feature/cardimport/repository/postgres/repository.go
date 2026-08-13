package cardimport_postgres_repository

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Repository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("cardimport PostgreSQL pool is nil")
	}

	return &Repository{pool: pool}
}
