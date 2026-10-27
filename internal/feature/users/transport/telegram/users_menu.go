package users_transport_tg

import tele "gopkg.in/telebot.v3"

const usersMenuText = "👥 <b>Пользователи</b>\n\nВыберите действие:"

func (h *UsersTgHandler) showUsersMenu(ctx tele.Context) error {
	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(buttonListUsers),
		mainMenuRow(markup),
	)
	return ctx.EditOrSend(usersMenuText, markup)
}
