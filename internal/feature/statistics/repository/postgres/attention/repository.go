package statistics_postgres_attention

import core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"

type AttentionRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *AttentionRepository {
	return &AttentionRepository{pool: pool}
}
