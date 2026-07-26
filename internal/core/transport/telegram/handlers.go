package core_transport_telegram

import (
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
)

const mainMenuText = "🏠 <b>Главное меню</b>\n\nВыберите действие:"

type Handler struct {
	bot        *tele.Bot
	roleAccess *core_tg_middleware.RoleAccess
	menuItems  []menuItem
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

func (h *Handler) Register() {
	mainMenuButton := MainMenuButton()

	h.RegisterCallback(
		mainMenuButton,
		domain.RoleUser,
		h.handleMainMenu,
		core_tg_middleware.RequireSender,
	)
	h.bot.Handle(
		"/start",
		h.handleMainMenu,
		core_tg_middleware.RequireSender,
	)
}

// RegisterCallback регистрирует нативный callback endpoint Telebot.
// Middleware, которое не вызывает next, должно само ответить на callback.
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

func (h *Handler) handleMainMenu(ctx tele.Context) error {
	markup, err := h.mainMenuMarkup(ctx)
	if err != nil {
		return fmt.Errorf("build main menu markup: %w", err)
	}

	return ctx.EditOrSend(
		mainMenuText,
		markup,
	)
}
