package cabinetcopy

import (
	"context"
	"go.uber.org/zap"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cabinetcopy_postgres_repository "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/repository/postgres"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cabinetcopy_config_transport "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/transport/config"
	cabinetcopy_telegram_transport "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	tele "gopkg.in/telebot.v3"
)

type Config = cabinetcopy_config_transport.Config

func NewConfigMust() Config { return cabinetcopy_config_transport.Must() }

type Feature struct {
	service  *cabinetcopy_service.Service
	telegram *cabinetcopy_telegram_transport.Handler
}

func New(
	ctx context.Context,
	pool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	bot *tele.Bot,
	reader cabinetcopy_service.Reader,
	batches *cardimport_service.Service,
	transfers *transfer_service.Service,
	config Config,
	loggers ...*zap.Logger,
) *Feature {
	if ctx == nil || pool == nil || uow == nil || bot == nil || reader == nil ||
		batches == nil || transfers == nil {
		panic("cabinet copy feature dependency is nil")
	}
	if err := config.Validate(); err != nil {
		panic(err)
	}
	repository := cabinetcopy_postgres_repository.New(pool)
	service := cabinetcopy_service.New(
		repository,
		reader,
		batches,
		transfers,
		uow,
		config.MaxCards,
		loggers...,
	)
	return &Feature{
		service:  service,
		telegram: cabinetcopy_telegram_transport.New(ctx, bot, service),
	}
}

func (feature *Feature) Service() *cabinetcopy_service.Service { return feature.service }

func (feature *Feature) MenuButton() tele.Btn { return cabinetcopy_telegram_transport.MenuButton() }

func (feature *Feature) SetCompletionNavigator(navigator cabinetcopy_telegram_transport.CompletionNavigator) {
	feature.telegram.SetCompletionNavigator(navigator)
}

func (feature *Feature) SetProcessingNotifier(notifier cabinetcopy_telegram_transport.ProcessingNotifier) {
	feature.telegram.SetProcessingNotifier(notifier)
}

func (feature *Feature) RegisterTelegram(menu *core_transport_telegram.Handler) {
	feature.telegram.Register(menu)
}
