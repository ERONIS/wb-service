package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	telegram_server "github.com/ERONIS/wb-service/internal/core/transport/telegram/server"
	users "github.com/ERONIS/wb-service/internal/feature/users"
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

	// Telegram.

	telegramServer, err := telegram_server.New(
		telegram_server.NewConfigMust(),
		logger.Logger,
	)
	if err != nil {
		panic(fmt.Errorf("create Telegram server: %w", err))
	}

	bot := telegramServer.Bot()

	usersFeature := users.New(
		ctx,
		postgresPool,
	)
	// Telegram commands.

	telegramHandler := core_transport_telegram.Register(
		ctx,
		bot,
		usersFeature.Service(),
	)
	usersFeature.RegisterTelegram(telegramHandler)

	if err := telegramServer.Run(ctx); err != nil {
		panic(fmt.Errorf("run Telegram server: %w", err))
	}
}
