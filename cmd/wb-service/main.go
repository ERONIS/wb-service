package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	telegram_server "github.com/ERONIS/wb-service/internal/core/transport/telegram/server"
	wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	cardimport "github.com/ERONIS/wb-service/internal/feature/cardimport"
	users "github.com/ERONIS/wb-service/internal/feature/users"
	platform_outbox "github.com/ERONIS/wb-service/internal/platform/outbox"
	platform_runtime "github.com/ERONIS/wb-service/internal/platform/runtime"
	platform_transaction "github.com/ERONIS/wb-service/internal/platform/transaction"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	// Logger.

	logger, err := core_logger.NewLogger(
		core_logger.NewConfigMust(),
	)
	if err != nil {
		panic(fmt.Errorf("create logger: %w", err))
	}
	defer logger.Close()

	// PostgreSQL.

	postgresPool, err := core_postgres_pool.NewConnectionPool(
		core_postgres_pool.NewConfigMust(),
		ctx,
	)
	if err != nil {
		panic(fmt.Errorf("create PostgreSQL pool: %w", err))
	}
	defer postgresPool.Close()

	// Singleton runtime. The advisory lock is acquired before Telegram, HTTP or
	// background workers are constructed.

	runtimeManager := platform_runtime.NewManager(
		platform_runtime.NewPGXConnectionAcquirer(postgresPool.Pool),
	)
	runtimeGuard, err := runtimeManager.Acquire(ctx)
	if err != nil {
		panic(fmt.Errorf("acquire singleton runtime: %w", err))
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer closeCancel()

		if err := runtimeGuard.Close(closeContext); err != nil {
			logger.Logger.Error(
				"close singleton runtime",
				zap.Error(err),
			)
		}
	}()
	ctx = runtimeGuard.Context()
	uow := platform_transaction.New(postgresPool, pgx.TxOptions{})
	outboxWriter := platform_outbox.NewWriter(
		runtimeGuard.SingletonKey(),
		runtimeGuard.Epoch(),
	)

	// Telegram.

	telegramServer, err := telegram_server.New(
		telegram_server.NewConfigMust(),
		logger.Logger,
	)
	if err != nil {
		panic(fmt.Errorf("create Telegram server: %w", err))
	}

	// Wildberries.

	wbConfig := wbconfig.NewConfigMust()

	wbClientset, err := wb.NewForConfig(
		&wbConfig,
		logger.Logger,
	)
	if err != nil {
		panic(fmt.Errorf("create WB clientset: %w", err))
	}
	defer wbClientset.CloseIdleConnections()

	bot := telegramServer.Bot()

	usersFeature := users.New(
		ctx,
		postgresPool,
	)
	cardimportFeature := cardimport.New(
		ctx,
		postgresPool,
		uow,
		outboxWriter,
		bot,
	)
	// Telegram commands.

	telegramHandler := core_transport_telegram.Register(
		ctx,
		bot,
		usersFeature.Service(),
	)
	usersFeature.RegisterTelegram(telegramHandler)
	cardimportFeature.RegisterTelegram(telegramHandler)

	if err := telegramServer.Run(ctx); err != nil {
		panic(fmt.Errorf("run Telegram server: %w", err))
	}
}
