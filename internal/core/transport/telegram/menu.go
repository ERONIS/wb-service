package core_transport_telegram

import tele "gopkg.in/telebot.v3"

const (
	CallbackMainMenu = "core_main_menu"
	mainMenuColumns  = 1
)

// MenuItemVisibility определяет видимость пункта для текущего обновления.
type MenuItemVisibility func(ctx tele.Context) bool

type menuItem struct {
	button    tele.Btn
	isVisible MenuItemVisibility
}

// RegisterMenuItem добавляет кнопку в главное меню и регистрирует её handler.
// Вызывать до запуска bot.Start; порядок вызовов определяет порядок кнопок.
func (h *Handler) RegisterMenuItem(
	button tele.Btn,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	h.RegisterConditionalMenuItem(
		button,
		nil,
		handler,
		middlewares...,
	)
}

// RegisterConditionalMenuItem добавляет кнопку с условием видимости
// и регистрирует её handler.
func (h *Handler) RegisterConditionalMenuItem(
	button tele.Btn,
	isVisible MenuItemVisibility,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	h.menuItems = append(h.menuItems, menuItem{
		button:    button,
		isVisible: isVisible,
	})
	h.RegisterCallback(button, handler, middlewares...)
}

func (h *Handler) mainMenuMarkup(ctx tele.Context) *tele.ReplyMarkup {
	markup := h.bot.NewMarkup()
	buttons := make([]tele.Btn, 0, len(h.menuItems))

	for _, item := range h.menuItems {
		if item.isVisible == nil || item.isVisible(ctx) {
			buttons = append(buttons, item.button)
		}
	}

	markup.Inline(
		markup.Split(mainMenuColumns, buttons)...,
	)

	return markup
}
