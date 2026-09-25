package statistics

import (
	"context"
	"time"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_postgres_repository "github.com/ERONIS/wb-service/internal/feature/statistics/repository/postgres"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	statistics_telegram_transport "github.com/ERONIS/wb-service/internal/feature/statistics/transport/telegram"
	"go.uber.org/zap"

	tele "gopkg.in/telebot.v3"
)

type Feature struct {
	service      *statistics_service.Service
	telegram     *statistics_telegram_transport.Handler
	cardVerifier statistics_telegram_transport.CardVerifier
}

func New(
	ctx context.Context,
	pool core_postgres_pool.Pool,
	bot *tele.Bot,
	manualResolver *cardpublication_service.ManualResolver,
	batches statistics_telegram_transport.BatchReader,
	cabinetNames map[string]string,
	cardVerifier statistics_telegram_transport.CardVerifier,
) *Feature {
	if ctx == nil || pool == nil || bot == nil || manualResolver == nil ||
		batches == nil {
		panic("statistics dependency is nil")
	}
	repository := statistics_postgres_repository.New(pool)
	service := statistics_service.New(repository)
	return &Feature{
		service:      service,
		cardVerifier: cardVerifier,
		telegram: statistics_telegram_transport.New(
			ctx,
			bot,
			service,
			manualResolver,
			batches,
			cabinetNames,
			cardVerifier,
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

func (feature *Feature) RunAutoAttentionResolver(
	ctx context.Context,
	interval time.Duration,
	logger *zap.Logger,
) error {
	if feature.cardVerifier == nil {
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	feature.processAttentionOnce(ctx, logger)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			feature.processAttentionOnce(ctx, logger)
		}
	}
}

func (feature *Feature) processAttentionOnce(ctx context.Context, logger *zap.Logger) {
	items, err := feature.service.ListAttention(ctx, statistics_service.AttentionFilter{Limit: 100})
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("Failed to list attention items for auto-resolution", zap.Error(err))
		}
		return
	}
	if len(items) == 0 {
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		info, err := feature.cardVerifier.FindCardByVendorCode(ctx, item.CabinetID, item.VendorCode)
		if err != nil {
			logger.Warn("Failed to verify card in WB during auto-resolution",
				zap.String("cabinet_id", item.CabinetID),
				zap.String("vendor_code", item.VendorCode),
				zap.Error(err),
			)
			continue
		}
		if info != nil {
			if err := feature.service.ResolveVerifiedCard(ctx, item.ItemTargetID, info.NMID, info.IMTID, info.SubjectID); err != nil {
				logger.Error("Failed to resolve verified card in auto-resolution",
					zap.Int64("item_target_id", item.ItemTargetID),
					zap.Int64("nm_id", info.NMID),
					zap.Error(err),
				)
				continue
			}
			logger.Info("Card automatically verified in WB and resolved",
				zap.String("cabinet_id", item.CabinetID),
				zap.String("vendor_code", item.VendorCode),
				zap.Int64("nm_id", info.NMID),
			)
		} else {
			if err := feature.service.RequeueCardForCreation(ctx, item.ItemTargetID); err != nil {
				logger.Error("Failed to requeue missing card for creation in auto-resolution",
					zap.Int64("item_target_id", item.ItemTargetID),
					zap.String("vendor_code", item.VendorCode),
					zap.Error(err),
				)
				continue
			}
			logger.Info("Card not found in WB, automatically requeued for creation",
				zap.String("cabinet_id", item.CabinetID),
				zap.String("vendor_code", item.VendorCode),
			)
		}
	}
}
