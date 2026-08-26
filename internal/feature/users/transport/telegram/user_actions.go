package users_transport_tg

import (
	"fmt"
	"html"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

func (h *UsersTgHandler) setAdmin(
	ctx tele.Context,
	targetTelegramID int64,
	page int,
) error {
	user, err := h.usersService.SetAdmin(
		h.ctx,
		ctx.Sender().ID,
		targetTelegramID,
	)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return ctx.EditOrSend(
		"✅ Пользователь назначен администратором.\n\n"+
			userCardText(user),
		userCardMarkup(user, page),
	)
}

func (h *UsersTgHandler) confirmDeleteUser(
	ctx tele.Context,
	targetTelegramID int64,
	page int,
) error {
	user, err := h.usersService.GetUser(
		h.ctx,
		ctx.Sender().ID,
		targetTelegramID,
	)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	if user.IsAdmin() {
		return core_transport_telegram.Notify(ctx, "users.admin_delete_forbidden", "⛔ Администраторов удалять нельзя.")
	}

	return ctx.EditOrSend(
		fmt.Sprintf(
			"⚠️ <b>Удалить пользователя?</b>\n\n"+
				"Имя: <b>%s</b>\n"+
				"TG ID: <code>%d</code>\n\n"+
				"Это действие нельзя отменить.",
			html.EscapeString(user.FullName),
			user.TelegramID,
		),
		deleteConfirmationMarkup(user.TelegramID, page),
	)
}

func (h *UsersTgHandler) deleteUserCallback(
	ctx tele.Context,
	targetTelegramID int64,
	page int,
) error {
	return h.deleteUser(
		ctx,
		targetTelegramID,
		deletedUserMarkup(page),
	)
}

func deleteConfirmationMarkup(
	telegramID int64,
	page int,
) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	payload := userPayload(telegramID, page)

	markup.Inline(
		markup.Row(
			markup.Data(
				buttonDeleteUser.Text,
				buttonDeleteUser.Unique,
				payload...,
			),
		),
		markup.Row(
			markup.Data(
				"↩️ Отмена",
				buttonGetUser.Unique,
				payload...,
			),
		),
	)

	return markup
}

func deletedUserMarkup(page int) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	markup.Inline(usersNavigationRows(markup, page)...)

	return markup
}
