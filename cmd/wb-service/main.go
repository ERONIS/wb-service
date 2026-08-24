package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_wb_identity_postgres "github.com/ERONIS/wb-service/internal/core/repository/postgres/wbidentity"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	telegram_server "github.com/ERONIS/wb-service/internal/core/transport/telegram/server"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	core_wb_config "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	cardimport "github.com/ERONIS/wb-service/internal/feature/cardimport"
	cardprepare "github.com/ERONIS/wb-service/internal/feature/cardprepare"
	cardprepare_wb_transport "github.com/ERONIS/wb-service/internal/feature/cardprepare/transport/wb"
	cardpublication "github.com/ERONIS/wb-service/internal/feature/cardpublication"
	cardpublication_wb_transport "github.com/ERONIS/wb-service/internal/feature/cardpublication/transport/wb"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer"
	transfer_wb_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/wb"
	users "github.com/ERONIS/wb-service/internal/feature/users"

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

	uow := core_postgres_transaction.New(postgresPool, pgx.TxOptions{})

	// Wildberries.

	wbConfig := core_wb_config.NewConfigMust()
	wbIdentityStore := core_wb_identity_postgres.New(uow)
	wbClientset, err := core_wb.NewForConfig(
		ctx,
		&wbConfig,
		wbIdentityStore,
		logger.Logger,
	)
	if err != nil {
		panic(fmt.Errorf("create WB clientset: %w", err))
	}
	defer wbClientset.CloseIdleConnections()

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
	cardimportFeature := cardimport.New(
		ctx,
		postgresPool,
		uow,
		bot,
	)
	transferFeature, err := transfer.New(
		ctx,
		postgresPool,
		uow,
		cardimportFeature.Service(),
		transfer_wb_transport.NewTargetTransport(wbClientset),
		transfer.NewConfigMust(),
	)
	if err != nil {
		panic(fmt.Errorf("create transfer feature: %w", err))
	}
	cardprepareFeature := cardprepare.New(
		postgresPool,
		uow,
		cardprepare_wb_transport.NewCatalogTransport(wbClientset),
		transferFeature.Service(),
		transferFeature.PreparationResultApplier(),
	)
	cardpublicationFeature := cardpublication.New(
		postgresPool,
		uow,
		cardpublication_wb_transport.NewCatalogTransport(wbClientset),
		transferFeature.Service(),
		transferFeature.PublicationResultApplier(),
		transferFeature.PublicationExecutionResultApplier(),
		cardprepareFeature.ProposalReader(),
		cardpublication.NewConfigMust(),
	)

	// Telegram commands.

	telegramHandler := core_transport_telegram.Register(
		ctx,
		bot,
		usersFeature.Service(),
	)
	usersFeature.RegisterTelegram(telegramHandler)
	cardimportFeature.RegisterTelegram(telegramHandler)
	transferFeature.ConfigureLiveAuthorization(
		ctx,
		bot,
		telegramHandler,
		cardpublicationFeature.LivePlanSource(),
	)
	cardpublicationFeature.ConfigureProductDispatch(
		transferFeature.LiveAuthorizationVerifier(),
	)

	go func() {
		if err := transferFeature.RunPolling(
			ctx,
			func(err error) {
				logger.Logger.Error("process pending transfers", zap.Error(err))
			},
			cardprepareFeature.Processor(),
			cardpublicationFeature.Processor(),
			cardpublicationFeature.ErrorFeed(),
			cardpublicationFeature.ProductDispatcher(),
			cardpublicationFeature.MediaDispatcher(),
		); err != nil && ctx.Err() == nil {
			logger.Logger.Error("run transfer polling", zap.Error(err))
			cancel()
		}
	}()

	if err := telegramServer.Run(ctx); err != nil {
		panic(fmt.Errorf("run Telegram server: %w", err))
	}
}
