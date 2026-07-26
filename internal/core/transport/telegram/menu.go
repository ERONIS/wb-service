package core_transport_telegram

import (
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
)

const (
	mainMenuText     = "🏠 <b>Главное меню</b>\n\nВыберите действие:"
	CallbackMainMenu = "core_main_menu"
	mainMenuColumns  = 1
)

type menuItem struct {
	button      tele.Btn
	minimumRole domain.UserRole
}

// MainMenuButton возвращает кнопку перехода в главное меню.
func MainMenuButton() tele.Btn {
	return tele.Btn{
		Text:   "🏠 Главное меню",
		Unique: CallbackMainMenu,
	}
}

// RegisterMainMenu регистрирует команду и callback главного меню.
func (h *Handler) RegisterMainMenu() {
	mainMenuButton := MainMenuButton()

	h.RegisterCallback(
		mainMenuButton,
		domain.RoleUser,
		h.handleMainMenu,
	)

	h.RegisterHandler(
		"/start",
		domain.RoleUser,
		h.handleMainMenu,
	)
}

// RegisterMenuItem добавляет кнопку в главное меню
// и регистрирует её callback handler.
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

// Handler для меню Telegram.
func (h *Handler) handleMainMenu(
	ctx tele.Context,
) error {
	markup, err := h.mainMenuMarkup(ctx)
	if err != nil {
		return fmt.Errorf(
			"build main menu markup: %w",
			err,
		)
	}

	return ctx.EditOrSend(
		mainMenuText,
		markup,
	)
}

// mainMenuMarkup строит меню для пользователя с учётом его роли.
func (h *Handler) mainMenuMarkup(
	ctx tele.Context,
) (*tele.ReplyMarkup, error) {
	markup := h.bot.NewMarkup()
	buttons := make(
		[]tele.Btn,
		0,
		len(h.menuItems),
	)

	actualRole, found, err := h.roleAccess.GetRole(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"get role for main menu: %w",
			err,
		)
	}

	for _, item := range h.menuItems {
		if found && core_tg_middleware.HasMinimumRole(
			actualRole,
			item.minimumRole,
		) {
			buttons = append(
				buttons,
				item.button,
			)
		}
	}

	markup.Inline(
		markup.Split(
			mainMenuColumns,
			buttons,
		)...,
	)

	return markup, nil
}
