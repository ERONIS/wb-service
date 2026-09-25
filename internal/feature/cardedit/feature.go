package cardedit

import (
	"context"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	cardedit_config_transport "github.com/ERONIS/wb-service/internal/feature/cardedit/transport/config"
	cardedit_telegram_transport "github.com/ERONIS/wb-service/internal/feature/cardedit/transport/telegram"
	cardedit_wb_transport "github.com/ERONIS/wb-service/internal/feature/cardedit/transport/wb"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

type Config = cardedit_config_transport.Config

func NewConfigMust() Config { return cardedit_config_transport.Must() }

type Feature struct {
	service  *cardedit_service.Service
	telegram *cardedit_telegram_transport.Handler
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	executors core_wb.PinnedExecutorRegistry,
	cabinets *wbcabinet_service.Service,
	config Config,
	logger *zap.Logger,
) *Feature {
	if err := config.Validate(); err != nil {
		panic(err)
	}
	gateway := cardedit_wb_transport.NewGateway(executors, cabinets)
	service := cardedit_service.New(ctx, gateway, config.ServiceConfig(), logger)
	telegram := cardedit_telegram_transport.New(
		ctx,
		bot,
		service,
		cardimport_xlsx_transport.NewParser(),
		config.FlowTTL,
	)
	service.SetNotifier(telegram)
	return &Feature{service: service, telegram: telegram}
}

func (feature *Feature) RegisterTelegram(menu *core_transport_telegram.Handler) {
	feature.telegram.Register(menu)
}

func (feature *Feature) MenuButton() tele.Btn {
	return cardedit_telegram_transport.MenuButton()
}
