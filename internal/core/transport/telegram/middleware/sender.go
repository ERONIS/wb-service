package core_tg_middleware

import tele "gopkg.in/telebot.v3"

// RequireSender пропускает только обновления с корректным Telegram-отправителем.
func RequireSender(next tele.HandlerFunc) tele.HandlerFunc {
	return func(ctx tele.Context) error {
		sender := ctx.Sender()
		if sender == nil || sender.ID <= 0 {
			if ctx.Callback() != nil {
				return ctx.RespondAlert(
					"Не удалось определить Telegram-пользователя.",
				)
			}

			return ctx.EditOrSend(
				"Не удалось определить Telegram-пользователя.",
			)
		}

		return next(ctx)
	}
}
