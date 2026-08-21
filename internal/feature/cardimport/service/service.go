package cardimport_service

import core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"

type Service struct {
	repository Repository
	parser     FileParser
	uow        core_postgres_transaction.UnitOfWork
}

func New(
	repository Repository,
	parser FileParser,
	uow core_postgres_transaction.UnitOfWork,
) *Service {
	if repository == nil {
		panic("cardimport repository is nil")
	}
	if parser == nil {
		panic("cardimport file parser is nil")
	}
	if uow == nil {
		panic("cardimport unit of work is nil")
	}
	return &Service{
		repository: repository,
		parser:     parser,
		uow:        uow,
	}
}
