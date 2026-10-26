package transfer_postgres_preparation

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"
)

type PreparationRepository struct {
	pool      core_postgres_pool.Pool
	transfers *transfer_postgres_workflow.WorkflowRepository
}

func New(pool core_postgres_pool.Pool, transfers *transfer_postgres_workflow.WorkflowRepository) *PreparationRepository {
	if pool == nil || transfers == nil {
		panic("transfer preparation PostgreSQL dependencies are nil")
	}
	return &PreparationRepository{pool: pool, transfers: transfers}
}
