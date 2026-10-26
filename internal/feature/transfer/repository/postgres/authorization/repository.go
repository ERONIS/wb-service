package transfer_postgres_authorization

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
)

type AuthorizationRepository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *AuthorizationRepository {
	if pool == nil {
		panic("transfer PostgreSQL pool is nil")
	}
	return &AuthorizationRepository{pool: pool}
}
