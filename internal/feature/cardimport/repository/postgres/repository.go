package cardimport_postgres_repository

import (
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	cardimport_author_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres/author"
	cardimport_batch_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres/batch"
)

type Repository struct {
	*cardimport_author_repository.Author
	*cardimport_batch_repository.Batch
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("cardimport PostgreSQL pool is nil")
	}
	return &Repository{
		Author: cardimport_author_repository.New(pool),
		Batch:  cardimport_batch_repository.New(pool),
	}
}
