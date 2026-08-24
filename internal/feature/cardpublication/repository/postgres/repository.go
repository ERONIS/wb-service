package cardpublication_postgres_repository

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type Repository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &Repository{pool: pool}
}
