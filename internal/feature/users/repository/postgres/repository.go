package users_postgres_repository

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	users_postgres_read "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/read"
	users_postgres_write "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres/write"
)

type Repository struct {
	*users_postgres_read.Read
	*users_postgres_write.Write
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("users PostgreSQL pool is nil")
	}
	return &Repository{
		Read:  users_postgres_read.New(pool),
		Write: users_postgres_write.New(pool),
	}
}
