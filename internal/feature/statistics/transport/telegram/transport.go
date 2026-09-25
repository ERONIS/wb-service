package statistics_telegram_transport

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

var (
	buttonStatistics     = tele.Btn{Text: "📊 Статистика", Unique: "stats_menu"}
	buttonOperation      = tele.Btn{Unique: "stats_operation"}
	buttonAction         = tele.Btn{Unique: "stats_action"}
	buttonAttention      = tele.Btn{Text: "⚠️ Требуют внимания", Unique: "stats_attention"}
	buttonAttentionItem  = tele.Btn{Unique: "stats_attention_item"}
	buttonCheckInWB      = tele.Btn{Text: "🔍 Проверить в WB", Unique: "stats_check_wb"}
	buttonCheckAllInWB   = tele.Btn{Text: "🔍 Проверить все в WB", Unique: "stats_check_all_wb"}
	buttonMarkPresent    = tele.Btn{Text: "✅ Подтвердить карточку", Unique: "stats_mark_present"}
	buttonMarkRejected   = tele.Btn{Text: "⛔ Подтвердить отклонение", Unique: "stats_mark_rejected"}
	buttonCloseAttention = tele.Btn{Text: "Оставить без отправки", Unique: "stats_close_attention"}
	buttonCurrentSession = tele.Btn{Text: "🔄 Обновить", Unique: "stats_current_session"}
	buttonCabinetTasks   = tele.Btn{Unique: "stats_cabinet_tasks"}
)

type CardVerificationInfo struct {
	NMID      int64
	IMTID     int64
	SubjectID int64
	Title     string
	Brand     string
}

type CardVerifier interface {
	FindCardByVendorCode(ctx context.Context, cabinetID string, vendorCode string) (*CardVerificationInfo, error)
}

type StatisticsService interface {
	ListOperations(context.Context, statistics_service.OperationFilter) ([]statistics_service.OperationRow, error)
	GetOperation(context.Context, int64) (statistics_service.OperationDetails, error)
	GetAction(context.Context, int64) (statistics_service.ActionDetails, error)
	ListCabinetProgress(context.Context, int64) ([]statistics_service.CabinetProgressRow, error)
	ListCabinetTasks(context.Context, statistics_service.CabinetTaskFilter) (statistics_service.CabinetTaskPage, error)
	AggregateTransfers(context.Context, statistics_service.AggregateFilter) (statistics_service.TransferTotals, error)
	AggregateItems(context.Context, statistics_service.AggregateFilter) (statistics_service.ItemTotals, error)
	AggregateActions(context.Context, statistics_service.AggregateFilter) (statistics_service.ActionTotals, error)
	AggregateErrors(context.Context, statistics_service.AggregateFilter) ([]statistics_service.ErrorGroup, error)
	ListAttention(context.Context, statistics_service.AttentionFilter) ([]statistics_service.AttentionRow, error)
	ResolveVerifiedCard(context.Context, int64, int64, int64, int64) error
	RequeueCardForCreation(context.Context, int64) error
}

type ManualResolver interface {
	Resolve(
		context.Context,
		cardpublication_service.ManualTrustedActor,
		cardpublication_service.ManualResolutionCommand,
	) (cardpublication_service.ManualResolution, error)
}

type BatchReader interface {
	GetBatch(context.Context, cardimport_service.BatchID) (cardimport_service.BatchHeader, error)
}

type Handler struct {
	ctx            context.Context
	bot            *tele.Bot
	statistics     StatisticsService
	manualResolver ManualResolver
	batches        BatchReader
	cabinetNames   map[string]string
	cardVerifier   CardVerifier
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	statistics StatisticsService,
	manualResolver ManualResolver,
	batches BatchReader,
	cabinetNames map[string]string,
	cardVerifier CardVerifier,
) *Handler {
	if ctx == nil || bot == nil || statistics == nil || manualResolver == nil ||
		batches == nil {
		panic("statistics Telegram dependency is nil")
	}
	names := make(map[string]string, len(cabinetNames))
	for id, name := range cabinetNames {
		names[id] = name
	}
	return &Handler{
		ctx:            ctx,
		bot:            bot,
		statistics:     statistics,
		manualResolver: manualResolver,
		batches:        batches,
		cabinetNames:   names,
		cardVerifier:   cardVerifier,
	}
}

func (handler *Handler) Register(menu *core_transport_telegram.Handler) {
	menu.RegisterMenuItem(buttonStatistics, domain.RoleAdmin, handler.showDashboard)
	menu.RegisterCallback(buttonOperation, domain.RoleAdmin, handler.openOperation)
	menu.RegisterCallback(buttonAction, domain.RoleAdmin, handler.openAction)
	menu.RegisterCallback(buttonAttention, domain.RoleAdmin, handler.listAttention)
	menu.RegisterCallback(buttonAttentionItem, domain.RoleAdmin, handler.openAttention)
	menu.RegisterCallback(buttonCheckInWB, domain.RoleAdmin, handler.checkAttention)
	menu.RegisterCallback(buttonCheckAllInWB, domain.RoleAdmin, handler.checkAllAttention)
	menu.RegisterCallback(buttonMarkPresent, domain.RoleAdmin, handler.markPresent)
	menu.RegisterCallback(buttonMarkRejected, domain.RoleAdmin, handler.markRejected)
	menu.RegisterCallback(buttonCloseAttention, domain.RoleAdmin, handler.closeAttention)
	menu.RegisterCallback(buttonCurrentSession, domain.RoleAdmin, handler.openCurrentSession)
	menu.RegisterCallback(buttonCabinetTasks, domain.RoleAdmin, handler.openCabinetTasks)
}
