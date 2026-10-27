package transfer_postgres_repository

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	transfer_postgres_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/authorization"
	transfer_postgres_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/preparation"
	transfer_postgres_publication "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/publication"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"
)

type Repository struct {
	*transfer_postgres_workflow.WorkflowRepository
	*transfer_postgres_preparation.PreparationRepository
	*transfer_postgres_authorization.AuthorizationRepository
	*transfer_postgres_publication.PublicationRepository
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	transfers := transfer_postgres_workflow.New(pool)
	return &Repository{
		WorkflowRepository:      transfers,
		PreparationRepository:   transfer_postgres_preparation.New(pool, transfers),
		AuthorizationRepository: transfer_postgres_authorization.New(pool),
		PublicationRepository:   transfer_postgres_publication.New(pool, transfers),
	}
}
