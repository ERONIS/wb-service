package transfer_telegram_transport

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	tele "gopkg.in/telebot.v3"
)

var (
	buttonTransfers = tele.Btn{
		Text:   "🚚 Отправка в WB",
		Unique: "transfer_live_menu",
	}
	buttonOpenPlan = tele.Btn{
		Text:   "Открыть план",
		Unique: "transfer_live_open",
	}
	buttonApprove = tele.Btn{
		Text:   "✅ Отправить в WB",
		Unique: "transfer_live_approve",
	}
	buttonRevoke = tele.Btn{
		Text:   "❌ Отозвать разрешение",
		Unique: "transfer_live_revoke",
	}
)

type LiveAuthorizationService interface {
	LiveEnabled() bool
	Request(
		ctx context.Context,
		actor transfer_service.LiveTrustedActor,
		command transfer_service.RequestLiveCommand,
	) (transfer_service.LiveAuthorization, error)
	Approve(
		ctx context.Context,
		actor transfer_service.LiveTrustedActor,
		command transfer_service.ApproveLiveCommand,
	) (transfer_service.LiveAuthorization, error)
	Revoke(
		ctx context.Context,
		actor transfer_service.LiveTrustedActor,
		command transfer_service.RevokeLiveCommand,
	) (transfer_service.LiveAuthorization, error)
}

type Handler struct {
	ctx           context.Context
	bot           *tele.Bot
	authorization LiveAuthorizationService
	plans         transfer_service.LivePlanSource
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	authorization LiveAuthorizationService,
	plans transfer_service.LivePlanSource,
) *Handler {
	if ctx == nil || bot == nil || authorization == nil || plans == nil {
		panic("transfer Telegram dependency is nil")
	}
	return &Handler{
		ctx:           ctx,
		bot:           bot,
		authorization: authorization,
		plans:         plans,
	}
}

func (handler *Handler) Register(menu *core_transport_telegram.Handler) {
	menu.RegisterMenuItem(
		buttonTransfers,
		domain.RoleAdmin,
		handler.showPlans,
	)
	menu.RegisterCallback(
		buttonOpenPlan,
		domain.RoleAdmin,
		handler.openPlan,
	)
	menu.RegisterCallback(
		buttonApprove,
		domain.RoleAdmin,
		handler.approvePlan,
	)
	menu.RegisterCallback(
		buttonRevoke,
		domain.RoleAdmin,
		handler.revokePlan,
	)
}
