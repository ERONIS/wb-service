package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_telegramview "github.com/ERONIS/wb-service/internal/core/repository/postgres/telegramview"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	telegram_server "github.com/ERONIS/wb-service/internal/core/transport/telegram/server"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardimport "github.com/ERONIS/wb-service/internal/feature/cardimport"
	cardprepare "github.com/ERONIS/wb-service/internal/feature/cardprepare"
	cardprepare_wb_transport "github.com/ERONIS/wb-service/internal/feature/cardprepare/transport/wb"
	cardpublication "github.com/ERONIS/wb-service/internal/feature/cardpublication"
	cardpublication_wb_transport "github.com/ERONIS/wb-service/internal/feature/cardpublication/transport/wb"
	statistics "github.com/ERONIS/wb-service/internal/feature/statistics"
	statistics_wb_transport "github.com/ERONIS/wb-service/internal/feature/statistics/transport/wb"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer"
	transfer_wb_transport "github.com/ERONIS/wb-service/internal/feature/transfer/transport/wb"
	users "github.com/ERONIS/wb-service/internal/feature/users"
	wbcabinet "github.com/ERONIS/wb-service/internal/feature/wbcabinet"

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

	logger, err := core_logger.NewLogger(
		core_logger.NewConfigMust(),
	)
	if err != nil {
		panic(fmt.Errorf("create logger: %w", err))
	}
	defer logger.Close()

	postgresPool, err := core_postgres_pool.NewConnectionPool(
		core_postgres_pool.NewConfigMust(),
		ctx,
		logger.Logger,
	)
	if err != nil {
		panic(fmt.Errorf("create PostgreSQL pool: %w", err))
	}
	defer postgresPool.Close()

	uow := core_postgres_transaction.New(postgresPool, pgx.TxOptions{})

	wbClientset, err := core_wb.NewClientset(
		ctx,
		logger.Logger,
		contentapi.BucketCatalog(),
	)
	if err != nil {
		panic(fmt.Errorf("create WB clientset: %w", err))
	}
	defer wbClientset.CloseIdleConnections()
	wbCabinetFeature, err := wbcabinet.New(
		ctx,
		postgresPool,
		uow,
		wbClientset,
	)
	if err != nil {
		panic(fmt.Errorf("create WB cabinet feature: %w", err))
	}
	if err := wbCabinetFeature.Initialize(ctx); err != nil {
		logger.Logger.Warn(
			"some WB cabinets were not restored",
			zap.Error(err),
		)
	}

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
		logger.Logger,
	)
	transferFeature, err := transfer.New(
		ctx,
		postgresPool,
		uow,
		cardimportFeature.Service(),
		transfer_wb_transport.NewTargetTransport(wbCabinetFeature.Service()),
		transfer.NewConfigMust(),
		logger.Logger,
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
		logger.Logger,
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
		logger.Logger,
	)
	livePlans := cardpublicationFeature.LivePlanSource()
	transferFeature.ConfigureLiveAuthorization(livePlans)
	liveAuthorization := transferFeature.LiveAuthorizationVerifier()
	activeCabinets := wbClientset.Cabinets()
	cabinetNames := make(map[string]string, len(activeCabinets))
	for _, cabinet := range activeCabinets {
		cabinetNames[string(cabinet.ID)] = cabinet.Name
	}
	statisticsFeature := statistics.New(
		ctx,
		postgresPool,
		bot,
		cardpublicationFeature.ManualResolver(),
		cardpublicationFeature.VerifiedCardResolver(),
		cardimportFeature.Service(),
		cabinetNames,
		statistics_wb_transport.NewVerifier(wbClientset),
	)
	cardimportFeature.SetCompletionNavigator(statisticsFeature.CompletionNavigator())
	cardimportFeature.SetProcessingNotifier(transferFeature)

	telegramHandler := core_tg_menu.Register(
		ctx,
		bot,
		usersFeature.Service(),
		core_postgres_telegramview.New(postgresPool),
	)
	usersFeature.RegisterTelegram(telegramHandler)
	wbCabinetFeature.RegisterTelegram(telegramHandler, usersFeature.Service())
	cardimportFeature.RegisterTelegram(telegramHandler)
	statisticsFeature.RegisterTelegram(telegramHandler)
	cardpublicationFeature.ConfigureProductDispatch(
		liveAuthorization,
	)
	var background sync.WaitGroup
	defer func() {
		cancel()
		background.Wait()
	}()
	background.Add(4)
	go func() {
		defer background.Done()
		wbCabinetFeature.RunRestoreRetry(ctx, 30*time.Second, logger.Logger)
	}()
	go func() {
		defer background.Done()
		statisticsFeature.RunAutoAttentionResolver(ctx, 10*time.Second, logger.Logger)
	}()
	go func() {
		defer background.Done()
		if err := cardpublicationFeature.RunMediaPolling(
			ctx,
			func(err error) {
				logger.Logger.Error("process pending publication media", zap.Error(err))
			},
		); err != nil && ctx.Err() == nil {
			logger.Logger.Error("run publication media polling", zap.Error(err))
		}
	}()

	go func() {
		defer background.Done()
		if err := transferFeature.RunPolling(
			ctx,
			func(err error) {
				logger.Logger.Error("process pending transfers", zap.Error(err))
			},
			cardprepareFeature.Processor(),
			cardpublicationFeature.Processor(),
			cardpublicationFeature.ErrorFeed(),
			transferFeature.AutomaticAuthorizationProcessor(),
			cardpublicationFeature.ProductDispatcher(),
		); err != nil && ctx.Err() == nil {
			logger.Logger.Error("run transfer polling", zap.Error(err))
			cancel()
		}
	}()

	if err := telegramServer.Run(ctx); err != nil {
		panic(fmt.Errorf("run Telegram server: %w", err))
	}
}
