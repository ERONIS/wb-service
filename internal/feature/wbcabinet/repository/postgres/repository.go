package wbcabinet_postgres_repository

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type Repository struct {
	pool core_postgres_pool.Pool
	uow  core_postgres_transaction.UnitOfWork
}

func New(
	pool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
) *Repository {
	if pool == nil || uow == nil {
		panic("wbcabinet PostgreSQL dependency is nil")
	}
	return &Repository{pool: pool, uow: uow}
}
