package cardimport

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	cardimport_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres"
	cardimport_flow "github.com/ERONIS/wb-service/internal/feature/cardimport/service/importflow"
	cardimport_telegram_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/telegram"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

// Feature объединяет зависимости модуля импорта карточек.
type Feature struct {
	service         *cardimport_flow.Service
	telegramHandler *cardimport_telegram_transport.Handler
}

func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	bot *tele.Bot,
	loggers ...*zap.Logger,
) *Feature {
	repository := cardimport_postgres_repository.New(postgresPool)
	parser := cardimport_xlsx_transport.NewParser()
	service := cardimport_flow.New(repository, parser, uow)

	return &Feature{
		service: service,
		telegramHandler: cardimport_telegram_transport.New(
			ctx,
			bot,
			service,
			loggers...,
		),
	}
}

func (f *Feature) Service() *cardimport_flow.Service {
	return f.service
}

func (f *Feature) SetCompletionNavigator(
	navigator cardimport_telegram_transport.CompletionNavigator,
) {
	f.telegramHandler.SetCompletionNavigator(navigator)
}

func (f *Feature) RegisterTelegram(
	menu *core_tg_menu.Handler,
) {
	f.telegramHandler.Register(menu)
}
