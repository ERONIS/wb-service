package transfer_postgres_publication

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"
)

type PublicationRepository struct {
	pool      core_postgres_pool.Pool
	transfers *transfer_postgres_workflow.WorkflowRepository
}

func New(pool core_postgres_pool.Pool, transfers *transfer_postgres_workflow.WorkflowRepository) *PublicationRepository {
	if pool == nil || transfers == nil {
		panic("transfer publication PostgreSQL dependencies are nil")
	}
	return &PublicationRepository{pool: pool, transfers: transfers}
}
