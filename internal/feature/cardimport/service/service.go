package cardimport_service

import (
	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"
)

type Service struct {
	repository Repository
	parser     FileParser
	uow        platform_transaction.UnitOfWork
	outbox     platform_outbox.Appender
}

func New(
	repository Repository,
	parser FileParser,
	uow platform_transaction.UnitOfWork,
	outbox platform_outbox.Appender,
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
	if outbox == nil {
		panic("cardimport outbox appender is nil")
	}

	return &Service{
		repository: repository,
		parser:     parser,
		uow:        uow,
		outbox:     outbox,
	}
}
