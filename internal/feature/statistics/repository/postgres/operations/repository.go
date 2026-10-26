package statistics_postgres_operations

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type OperationsRepository struct {
	pool        core_postgres_pool.Pool
	snapshotUoW *core_postgres_transaction.PGXUnitOfWork
}

func New(pool core_postgres_pool.Pool, snapshotUoW *core_postgres_transaction.PGXUnitOfWork) *OperationsRepository {
	return &OperationsRepository{pool: pool, snapshotUoW: snapshotUoW}
}
