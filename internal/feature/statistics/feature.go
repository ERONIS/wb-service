package statistics

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_postgres_repository "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	statistics_telegram_transport "github.com/ERONIS/wb-service/internal/feature/statistics/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

type Feature struct {
	service  *statistics_service.Service
	telegram *statistics_telegram_transport.Handler
}

func New(
	ctx context.Context,
	pool core_postgres_pool.Pool,
	bot *tele.Bot,
	manualResolver *cardpublication_service.ManualResolver,
	batches statistics_telegram_transport.BatchReader,
	cabinetNames map[string]string,
) *Feature {
	if ctx == nil || pool == nil || bot == nil || manualResolver == nil ||
		batches == nil {
		panic("statistics dependency is nil")
	}
	repository := statistics_postgres_repository.New(pool)
	service := statistics_service.New(repository)
	return &Feature{
		service: service,
		telegram: statistics_telegram_transport.New(
			ctx,
			bot,
			service,
			manualResolver,
			batches,
			cabinetNames,
		),
	}
}

func (feature *Feature) CompletionNavigator() *statistics_telegram_transport.Handler {
	return feature.telegram
}

func (feature *Feature) Service() *statistics_service.Service {
	return feature.service
}

func (feature *Feature) RegisterTelegram(menu *core_transport_telegram.Handler) {
	feature.telegram.Register(menu)
}
