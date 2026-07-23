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
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"
	telegram_server "github.com/ERONIS/wb-service/internal/core/transport/telegram/server"
	users_postgres_repository "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"
	users_transport_tg "github.com/ERONIS/wb-service/internal/feature/users/transport/telegram"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "run application:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	// Logger.

	loggerConfig, err := core_logger.NewConfig()
	if err != nil {
		return fmt.Errorf(
			"create logger config: %w",
			err,
		)
	}

	logger, err := core_logger.NewLogger(loggerConfig)
	if err != nil {
		return fmt.Errorf(
			"create logger: %w",
			err,
		)
	}
	defer logger.Close()

	// PostgreSQL.

	postgresConfig, err := core_postgres_pool.NewConfig()
	if err != nil {
		return fmt.Errorf(
			"create PostgreSQL config: %w",
			err,
		)
	}

	postgresPool, err := core_postgres_pool.NewConnectionPool(
		postgresConfig,
		ctx,
	)
	if err != nil {
		return fmt.Errorf(
			"create PostgreSQL pool: %w",
			err,
		)
	}
	defer postgresPool.Close()

	// Telegram.

	telegramConfig, err := telegram_server.NewConfig()
	if err != nil {
		return fmt.Errorf(
			"create Telegram config: %w",
			err,
		)
	}

	telegramServer, err := telegram_server.New(
		telegramConfig,
	)
	if err != nil {
		return fmt.Errorf(
			"create Telegram server: %w",
			err,
		)
	}

	bot := telegramServer.Bot()
	bot.Use(core_tg_middleware.Logger(logger.Logger))

	// Users feature:
	// PostgreSQL repository → service → Telegram handler.

	usersRepository :=
		users_postgres_repository.NewUsersRepository(
			postgresPool,
		)

	usersService :=
		users_service.NewUsersService(
			usersRepository,
		)

	usersTelegramHandler :=
		users_transport_tg.NewUsersTgHandler(
			ctx,
			usersService,
		)

	// Общие Telegram-команды.

	telegramHandler :=
		core_transport_telegram.NewHandler(bot)

	telegramHandler.Register()

	// Users-команды.

	usersTelegramHandler.Register(
		bot,
		telegramHandler,
	)

	if err := telegramServer.Run(ctx); err != nil {
		return fmt.Errorf(
			"run Telegram server: %w",
			err,
		)
	}

	return nil
}
