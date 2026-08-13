package cardimport_telegram_transport

import (
	"context"
	"io"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

var (
	buttonCards = tele.Btn{
		Text:   "📦 Карточки",
		Unique: "cardimport_menu",
	}
	buttonTransfer = tele.Btn{
		Text:   "📤 Перенос карточек",
		Unique: "cardimport_transfer",
	}
	buttonCancel = tele.Btn{
		Text:   "✖️ Отменить импорт",
		Unique: "cardimport_cancel",
	}
	buttonFinalize = tele.Btn{
		Text:   "✅ Готово",
		Unique: "cardimport_finalize",
	}
)

type CardImportService interface {
	Begin(
		ctx context.Context,
		command cardimport_service.BeginCommand,
	) (cardimport_service.Session, error)

	GetActiveView(
		ctx context.Context,
		authorTelegramID int64,
	) (cardimport_service.SessionView, error)

	GetView(
		ctx context.Context,
		command cardimport_service.SessionCommand,
	) (cardimport_service.SessionView, error)

	Cancel(
		ctx context.Context,
		command cardimport_service.SessionCommand,
	) error

	ReserveFile(
		ctx context.Context,
		command cardimport_service.ReserveFileCommand,
	) (cardimport_service.File, error)

	StoreFile(
		ctx context.Context,
		command cardimport_service.StoreFileCommand,
		source io.Reader,
	) (cardimport_service.File, error)

	ParseFile(
		ctx context.Context,
		command cardimport_service.ParseFileCommand,
	) (cardimport_service.SessionView, error)

	Finalize(
		ctx context.Context,
		actor cardimport_service.TrustedActor,
		command cardimport_service.FinalizeCommand,
	) (cardimport_service.BatchHeader, error)
}

type Handler struct {
	ctx     context.Context
	bot     *tele.Bot
	service CardImportService
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	service CardImportService,
) *Handler {
	if ctx == nil {
		panic("cardimport Telegram context is nil")
	}
	if bot == nil {
		panic("cardimport Telegram bot is nil")
	}
	if service == nil {
		panic("cardimport Telegram service is nil")
	}

	return &Handler{
		ctx:     ctx,
		bot:     bot,
		service: service,
	}
}

func (h *Handler) Register(menu *core_transport_telegram.Handler) {
	menu.RegisterMenuItem(
		buttonCards,
		domain.RoleAdmin,
		h.showCardsMenu,
	)
	menu.RegisterCallback(
		buttonTransfer,
		domain.RoleAdmin,
		h.startTransfer,
	)
	menu.RegisterCallback(
		buttonCancel,
		domain.RoleAdmin,
		h.cancel,
	)
	menu.RegisterCallback(
		buttonFinalize,
		domain.RoleAdmin,
		h.finalize,
	)
	menu.RegisterHandler(
		tele.OnDocument,
		domain.RoleAdmin,
		h.receiveDocument,
	)
}
