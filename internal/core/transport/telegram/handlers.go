package core_transport_telegram

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
)

type Handler struct {
	bot        *tele.Bot
	roleAccess *core_tg_middleware.RoleAccess
	menuItems  []menuItem
}

// Register создаёт Telegram-обработчик и регистрирует главное меню.
func Register(
	ctx context.Context,
	bot *tele.Bot,
	roleProvider core_tg_middleware.RoleProvider,
) *Handler {
	roleAccess := core_tg_middleware.NewRoleAccess(
		ctx,
		roleProvider,
	)
	handler := NewHandler(bot, roleAccess)
	handler.RegisterMainMenu()

	return handler
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

	return &Handler{
		bot:        bot,
		roleAccess: roleAccess,
		menuItems:  make([]menuItem, 0),
	}
}

// RegisterCallback регистрирует нативный callback endpoint Telebot.
func (h *Handler) RegisterCallback(
	button tele.Btn,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	middlewares = append(
		middlewares,
		h.roleAccess.Require(minimumRole),
	)

	h.bot.Handle(
		&button,
		handler,
		core_tg_middleware.Callback(middlewares...)...,
	)
}
