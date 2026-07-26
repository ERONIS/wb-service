package core_transport_telegram

import (
	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
)

const (
	CallbackMainMenu = "core_main_menu"
	mainMenuColumns  = 1
)

type menuItem struct {
	button      tele.Btn
	minimumRole domain.UserRole
}

// RegisterMenuItem добавляет кнопку в главное меню и регистрирует её handler.
// Вызывать до запуска bot.Start; порядок вызовов определяет порядок кнопок.
func (h *Handler) RegisterMenuItem(
	button tele.Btn,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	h.menuItems = append(h.menuItems, menuItem{
		button:      button,
		minimumRole: minimumRole,
	})
	h.RegisterCallback(
		button,
		minimumRole,
		handler,
		middlewares...,
	)
}

func (h *Handler) mainMenuMarkup(
	ctx tele.Context,
) (*tele.ReplyMarkup, error) {
	markup := h.bot.NewMarkup()
	buttons := make([]tele.Btn, 0, len(h.menuItems))
	actualRole, found, err := h.roleAccess.GetRole(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range h.menuItems {
		if found && core_tg_middleware.HasMinimumRole(
			actualRole,
			item.minimumRole,
		) {
			buttons = append(buttons, item.button)
		}
	}

	markup.Inline(
		markup.Split(mainMenuColumns, buttons)...,
	)

	return markup, nil
}
