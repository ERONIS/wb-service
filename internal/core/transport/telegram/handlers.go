package core_transport_telegram

import (
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
)

const mainMenuText = "🏠 <b>Главное меню</b>\n\nВыберите действие:"

type Handler struct {
	bot       *tele.Bot
	menuItems []menuItem
}

func NewHandler(bot *tele.Bot) *Handler {
	if bot == nil {
		panic("telegram bot is nil")
	}

	return &Handler{
		bot:       bot,
		menuItems: make([]menuItem, 0),
	}
}

func (h *Handler) Register() {
	mainMenuButton := MainMenuButton()

	h.RegisterCallback(
		mainMenuButton,
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
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	h.bot.Handle(
		&button,
		handler,
		core_tg_middleware.Callback(middlewares...)...,
	)
}

func (h *Handler) handleMainMenu(ctx tele.Context) error {
	return ctx.EditOrSend(
		mainMenuText,
		h.mainMenuMarkup(ctx),
	)
}
