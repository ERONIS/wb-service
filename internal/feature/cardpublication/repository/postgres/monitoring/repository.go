package monitoring

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type MonitoringRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *MonitoringRepository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &MonitoringRepository{pool: pool}
}
