package cabinetcopy_telegram_transport

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

var (
	buttonCabinetCopy = tele.Btn{Text: "🔁 Между кабинетами", Unique: "cabcopy_menu"}
	buttonSource      = tele.Btn{Unique: "cabcopy_source"}
	buttonTarget      = tele.Btn{Unique: "cabcopy_target"}
	buttonCount       = tele.Btn{Unique: "cabcopy_count"}
	buttonCustomCount = tele.Btn{Text: "Другое количество", Unique: "cabcopy_count_custom"}
	buttonTag         = tele.Btn{Unique: "cabcopy_tag"}
	buttonTagPage     = tele.Btn{Unique: "cabcopy_tag_page"}
	buttonPrepare     = tele.Btn{Unique: "cabcopy_prepare"}
	buttonSubmit      = tele.Btn{Text: "✅ Запустить", Unique: "cabcopy_submit"}
	buttonCancel      = tele.Btn{Text: "✖️ Отменить", Unique: "cabcopy_cancel"}
)

const countTextFlow = "cabinetcopy.count"

func MenuButton() tele.Btn { return buttonCabinetCopy }

type CompletionNavigator interface {
	ShowFinalizedSession(tele.Context, cardimport_service.BatchHeader) error
}

type ProcessingNotifier interface {
	NotifyFinalized()
}

type Handler struct {
	ctx        context.Context
	bot        *tele.Bot
	service    *cabinetcopy_service.Service
	completion CompletionNavigator
	processing ProcessingNotifier
	textFlows  *core_transport_telegram.Handler
}

func New(ctx context.Context, bot *tele.Bot, service *cabinetcopy_service.Service) *Handler {
	if ctx == nil || bot == nil || service == nil {
		panic("cabinet copy Telegram dependency is nil")
	}
	return &Handler{ctx: ctx, bot: bot, service: service}
}

func (handler *Handler) SetCompletionNavigator(navigator CompletionNavigator) {
	if navigator == nil || handler.completion != nil {
		panic("cabinet copy completion navigator is invalid")
	}
	handler.completion = navigator
}

func (handler *Handler) SetProcessingNotifier(notifier ProcessingNotifier) {
	if notifier == nil || handler.processing != nil {
		panic("cabinet copy processing notifier is invalid")
	}
	handler.processing = notifier
}

func (handler *Handler) Register(menu *core_transport_telegram.Handler) {
	if handler.completion == nil || handler.processing == nil {
		panic("cabinet copy Telegram integration is incomplete")
	}
	if menu == nil || handler.textFlows != nil {
		panic("cabinet copy Telegram menu integration is invalid")
	}
	handler.textFlows = menu
	menu.RegisterTextFlow(countTextFlow, domain.RoleAdmin, handler.receiveCount)
	menu.RegisterCallback(buttonCabinetCopy, domain.RoleAdmin, handler.begin)
	menu.RegisterCallback(buttonSource, domain.RoleAdmin, handler.selectSource)
	menu.RegisterCallback(buttonTarget, domain.RoleAdmin, handler.selectTarget)
	menu.RegisterCallback(buttonCount, domain.RoleAdmin, handler.selectCount)
	menu.RegisterCallback(buttonCustomCount, domain.RoleAdmin, handler.requestCustomCount)
	menu.RegisterCallback(buttonTag, domain.RoleAdmin, handler.toggleTag)
	menu.RegisterCallback(buttonTagPage, domain.RoleAdmin, handler.openTagPage)
	menu.RegisterCallback(buttonPrepare, domain.RoleAdmin, handler.prepare)
	menu.RegisterCallback(buttonSubmit, domain.RoleAdmin, handler.submit)
	menu.RegisterCallback(buttonCancel, domain.RoleAdmin, handler.cancel)
}
