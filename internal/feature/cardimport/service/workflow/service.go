package cardimport_workflow

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_staging "github.com/ERONIS/wb-service/internal/feature/cardimport/service/staging"
)

type Service struct {
	repository cardimport_contract.Repository
	parser     cardimport_contract.FileParser
	uow        core_postgres_transaction.UnitOfWork
	sessions   *cardimport_staging.Store
}

// New собирает сервис импорта с зависимостями и памятью сессий.
func New(
	repository cardimport_contract.Repository,
	parser cardimport_contract.FileParser,
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
		sessions:   cardimport_staging.NewStore(),
	}
}
