package core_tg_menu

import (
	"context"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
	tele_middleware "gopkg.in/telebot.v3/middleware"
)

type textFlowOwner struct {
	key string
	id  uint64
}

type Handler struct {
	bot        *tele.Bot
	roleAccess *core_tg_middleware.RoleAccess
	menuHost   *menuHost
	menuItems  []menuItem

	textMu       sync.RWMutex
	textHandlers map[string]inputFlow
	textOwners   map[int64]textFlowOwner
	nextFlowID   uint64

	documentFallback *inputFlow
	flowCleanups     map[string]func(int64)
}

type TextFlowHandler func(tele.Context) (handled bool, err error)

type inputFlow struct {
	minimumRole domain.UserRole
	handler     TextFlowHandler
}

// Register создаёт Telegram-обработчик и регистрирует главное меню.
func Register(
	ctx context.Context,
	bot *tele.Bot,
	roleProvider core_tg_middleware.RoleProvider,
	menuStore MenuStore,
) *Handler {
	roleAccess := core_tg_middleware.NewRoleAccess(
		ctx,
		roleProvider,
	)
	menuHost := newMenuHost(ctx, bot, menuStore)
	bot.Use(roleAccess.IgnoreWithoutRole(), menuHost.Middleware())
	handler := NewHandler(bot, roleAccess)
	handler.menuHost = menuHost
	handler.RegisterMainMenu()
	menuHost.setPersistentReplyKeyboard(
		replyKeyboardMessage,
		handler.mainMenuReplyMarkup,
	)
	menuHost.setInputFallback(
		roleAccess.Require(domain.RoleUser)(func(ctx tele.Context) error {
			return handler.renderMainMenu(ctx)
		}),
	)

	return handler
}

func (h *Handler) RenderChat(
	chatID int64,
	text string,
	opts ...interface{},
) error {
	if h == nil || h.menuHost == nil || chatID == 0 {
		return tele.ErrBadContext
	}
	return h.menuHost.renderChat(chatID, text, opts...)
}

// RegisterHandler регистрирует команду или Telegram event с проверкой роли.
func (h *Handler) RegisterHandler(
	endpoint any,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
) {
	h.bot.Handle(
		endpoint,
		handler,
		h.roleAccess.Require(minimumRole),
	)
}

func NewHandler(
	bot *tele.Bot,
	roleAccess *core_tg_middleware.RoleAccess,
) *Handler {
	if bot == nil {
		panic("telegram bot is nil")
	}
	if roleAccess == nil {
		panic("telegram role access is nil")
	}

	handler := &Handler{
		bot:          bot,
		roleAccess:   roleAccess,
		menuItems:    make([]menuItem, 0),
		textHandlers: make(map[string]inputFlow),
		textOwners:   make(map[int64]textFlowOwner),
		flowCleanups: make(map[string]func(int64)),
	}
	bot.Handle(tele.OnText, handler.handleTextFlow)
	bot.Handle(tele.OnDocument, handler.handleDocumentFlow)
	return handler
}

// RegisterCallback регистрирует callback с проверкой роли и завершением текстового ввода.
func (h *Handler) RegisterCallback(
	button tele.Btn,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	chain := append([]tele.MiddlewareFunc(nil), middlewares...)
	chain = append(
		chain,
		h.roleAccess.Require(minimumRole),
		tele_middleware.AutoRespond(),
	)
	h.bot.Handle(&button, func(ctx tele.Context) error {
		if sender := ctx.Sender(); sender != nil {
			h.EndTextFlows(sender.ID)
		}
		return handler(ctx)
	}, chain...)
}
