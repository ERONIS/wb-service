package statistics_postgres_cabinets

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type CabinetsRepository struct {
	pool        core_postgres_pool.Pool
	snapshotUoW *core_postgres_transaction.PGXUnitOfWork
}

func New(pool core_postgres_pool.Pool, snapshotUoW *core_postgres_transaction.PGXUnitOfWork) *CabinetsRepository {
	return &CabinetsRepository{pool: pool, snapshotUoW: snapshotUoW}
}
