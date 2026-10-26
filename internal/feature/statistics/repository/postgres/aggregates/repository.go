package statistics_postgres_aggregates

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type AggregatesRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *AggregatesRepository {
	return &AggregatesRepository{pool: pool}
}
