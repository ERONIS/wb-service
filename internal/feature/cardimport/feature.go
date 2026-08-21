package cardimport

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	cardimport_telegram_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/telegram"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	tele "gopkg.in/telebot.v3"
)

// Feature объединяет зависимости модуля импорта карточек.
type Feature struct {
	service         *cardimport_service.Service
	telegramHandler *cardimport_telegram_transport.Handler
}

func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	bot *tele.Bot,
) *Feature {
	repository := cardimport_postgres_repository.New(postgresPool)
	parser := cardimport_xlsx_transport.NewParser()
	service := cardimport_service.New(repository, parser, uow)

	return &Feature{
		service: service,
		telegramHandler: cardimport_telegram_transport.New(
			ctx,
			bot,
			service,
		),
	}
}

func (f *Feature) Service() *cardimport_service.Service {
	return f.service
}

func (f *Feature) RegisterTelegram(
	menu *core_transport_telegram.Handler,
) {
	f.telegramHandler.Register(menu)
}
