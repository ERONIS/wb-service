package core_tg_menu

import (
	"context"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
	tele_middleware "gopkg.in/telebot.v3/middleware"
)

type Handler struct {
	bot        *tele.Bot
	roleAccess *core_tg_middleware.RoleAccess
	menuHost   *menuHost
	menuItems  []menuItem

	textMu       sync.RWMutex
	textHandlers map[string]inputFlow
	textOwners   map[int64]string

	documentHandlers map[string]inputFlow
	documentFallback *inputFlow
	flowCleanups     map[string]func(int64)
}

type FlowHandler func(tele.Context) (handled bool, err error)

type TextFlowHandler = FlowHandler
type DocumentFlowHandler = FlowHandler

type inputFlow struct {
	minimumRole domain.UserRole
	handler     FlowHandler
}

// Register создаёт Telegram-обработчик и регистрирует главное меню.
func Register(
	ctx context.Context,
	bot *tele.Bot,
	roleProvider core_tg_middleware.RoleProvider,
	menuStore MenuStore,
) *Handler {
	menuHost := newMenuHost(ctx, bot, menuStore)
	bot.Use(menuHost.Middleware())
	roleAccess := core_tg_middleware.NewRoleAccess(
		ctx,
		roleProvider,
	)
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
		bot:              bot,
		roleAccess:       roleAccess,
		menuItems:        make([]menuItem, 0),
		textHandlers:     make(map[string]inputFlow),
		textOwners:       make(map[int64]string),
		documentHandlers: make(map[string]inputFlow),
		flowCleanups:     make(map[string]func(int64)),
	}
	bot.Handle(tele.OnText, handler.handleTextFlow)
	bot.Handle(tele.OnDocument, handler.handleDocumentFlow)
	return handler
}

// RegisterCallback регистрирует нативный callback endpoint Telebot.
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
	h.bot.Handle(&button, handler, chain...)
}
