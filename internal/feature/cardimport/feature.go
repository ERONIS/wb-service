package cardimport

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	cardimport_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_workflow "github.com/ERONIS/wb-service/internal/feature/cardimport/service/workflow"
	cardimport_telegram_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/telegram"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

// Feature объединяет зависимости модуля импорта карточек.
type Feature struct {
	service         *cardimport_workflow.Service
	telegramHandler *cardimport_telegram_transport.Handler
}

func New(
	postgresPool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
) *Feature {
	repository := cardimport_postgres_repository.New(postgresPool)
	parser := cardimport_xlsx_transport.NewParser()
	service := cardimport_workflow.New(repository, parser, uow)

	return &Feature{
		service: service,
	}
}

func (f *Feature) AttachTelegram(ctx context.Context, bot *tele.Bot, loggers ...*zap.Logger) {
	if f.telegramHandler != nil {
		panic("cardimport Telegram handler is already attached")
	}
	f.telegramHandler = cardimport_telegram_transport.New(ctx, bot, f.service, loggers...)
}

func (f *Feature) Service() *cardimport_workflow.Service {
	return f.service
}

func (f *Feature) BatchReader() cardimport_contract.BatchReader {
	return f.service
}

func (f *Feature) SetCompletionNavigator(
	navigator cardimport_telegram_transport.CompletionNavigator,
) {
	if f.telegramHandler == nil {
		panic("cardimport Telegram handler is not attached")
	}
	f.telegramHandler.SetCompletionNavigator(navigator)
}

func (f *Feature) RegisterTelegram(
	menu *core_tg_menu.Handler,
) {
	if f.telegramHandler == nil {
		panic("cardimport Telegram handler is not attached")
	}
	f.telegramHandler.Register(menu)
}
