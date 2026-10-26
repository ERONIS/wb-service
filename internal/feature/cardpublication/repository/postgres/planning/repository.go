package planning

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type PlanningRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *PlanningRepository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &PlanningRepository{pool: pool}
}
