package statistics_postgres_repository

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	statistics_postgres_aggregates "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres/aggregates"
	statistics_postgres_attention "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres/attention"
	statistics_postgres_cabinets "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres/cabinets"
	statistics_postgres_operations "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres/operations"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	"github.com/jackc/pgx/v5"
)

type Repository struct {
	*statistics_postgres_aggregates.AggregatesRepository
	*statistics_postgres_attention.AttentionRepository
	*statistics_postgres_cabinets.CabinetsRepository
	*statistics_postgres_operations.OperationsRepository
	snapshotUoW *core_postgres_transaction.PGXUnitOfWork
}

var _ statistics_service.Reader = (*Repository)(nil)

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("statistics PostgreSQL pool is nil")
	}
	snapshotUoW := core_postgres_transaction.New(pool, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	return &Repository{
		AggregatesRepository: statistics_postgres_aggregates.New(pool),
		AttentionRepository:  statistics_postgres_attention.New(pool),
		CabinetsRepository:   statistics_postgres_cabinets.New(pool, snapshotUoW),
		OperationsRepository: statistics_postgres_operations.New(pool, snapshotUoW),
		snapshotUoW:          snapshotUoW,
	}
}

// withinSnapshot выполняет связанные чтения на одном снимке PostgreSQL.
func (repository *Repository) withinSnapshot(
	ctx context.Context,
	fn func(context.Context, core_postgres_transaction.DBTX) error,
) error {
	return repository.snapshotUoW.WithinTransaction(ctx, fn)
}
