package transfer_postgres_workflow

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
)

const MaxTransferPageSize = 1000

type WorkflowRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *WorkflowRepository {
	if pool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	return &WorkflowRepository{pool: pool}
}
