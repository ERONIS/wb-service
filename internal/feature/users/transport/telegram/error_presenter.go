package users_transport_tg

import (
	"errors"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	tele "gopkg.in/telebot.v3"
)

func sendServiceError(
	ctx tele.Context,
	err error,
) error {
	return ctx.EditOrSend(serviceErrorText(err))
}

func serviceErrorText(err error) string {
	switch {
	case errors.Is(err, core_errors.ErrForbidden):
		return "⛔ Недостаточно прав."

	case errors.Is(err, core_errors.ErrNotFound):
		return "❌ Пользователь не найден."

	case errors.Is(err, core_errors.ErrConflict):
		return "⚠️ Операция не выполнена: данные уже существуют."

	case errors.Is(err, core_errors.ErrInvalidArgument):
		return "⚠️ Переданы некорректные данные."

	default:
		return "❌ Не удалось выполнить операцию. Попробуйте позже."
	}
}
