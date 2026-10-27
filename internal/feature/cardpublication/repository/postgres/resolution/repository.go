package resolution

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type ResolutionRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *ResolutionRepository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &ResolutionRepository{pool: pool}
}
