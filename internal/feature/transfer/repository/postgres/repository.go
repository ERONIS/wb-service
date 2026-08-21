package transfer_postgres_repository

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
	if pool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	if uow == nil {
		panic("transfer unit of work is nil")
	}
	return &Repository{pool: pool, uow: uow}
}
