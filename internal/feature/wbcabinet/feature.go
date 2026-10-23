package wbcabinet

import (
	"context"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	wbcabinet_postgres_repository "github.com/ERONIS/wb-service/internal/feature/wbcabinet/repository/postgres"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
	wbcabinet_telegram_transport "github.com/ERONIS/wb-service/internal/feature/wbcabinet/transport/telegram"
	wbcabinet_wb_transport "github.com/ERONIS/wb-service/internal/feature/wbcabinet/transport/wb"
	"go.uber.org/zap"
)

type Feature struct {
	service  *wbcabinet_service.Service
	telegram *wbcabinet_telegram_transport.Handler
}

func New(
	ctx context.Context,
	pool core_postgres_pool.Pool,
	uow core_postgres_transaction.UnitOfWork,
	runtime wbcabinet_wb_transport.Runtime,
) (*Feature, error) {
	repository := wbcabinet_postgres_repository.New(pool)
	verifier := wbcabinet_wb_transport.NewVerifier(runtime)
	service := wbcabinet_service.New(repository, verifier, uow)
	return &Feature{
		service:  service,
		telegram: wbcabinet_telegram_transport.New(ctx, service),
	}, nil
}

func (feature *Feature) Initialize(ctx context.Context) error {
	return feature.service.Restore(ctx)
}

func (feature *Feature) Service() *wbcabinet_service.Service {
	return feature.service
}

func (feature *Feature) RunRestoreRetry(
	ctx context.Context,
	interval time.Duration,
	logger *zap.Logger,
) {
	feature.service.RunRestoreRetry(ctx, interval, logger)
}

type ProfileProvider interface {
	GetProfile(context.Context, int64) (domain.User, error)
}

func (feature *Feature) RegisterTelegram(
	menu *core_tg_menu.Handler,
	profiles ProfileProvider,
) {
	feature.telegram.Register(menu, profiles)
}
