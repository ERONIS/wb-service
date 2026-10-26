package cardpublication_postgres_repository

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres/execution"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres/monitoring"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres/planning"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/repository/postgres/resolution"
)

type Repository struct {
	*planning.PlanningRepository
	*execution.ExecutionRepository
	*monitoring.MonitoringRepository
	*resolution.ResolutionRepository
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("cardpublication PostgreSQL pool is nil")
	}
	return &Repository{
		PlanningRepository:   planning.New(pool),
		ExecutionRepository:  execution.New(pool),
		MonitoringRepository: monitoring.New(pool),
		ResolutionRepository: resolution.New(pool),
	}
}
