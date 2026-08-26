package users_transport_tg

import (
	"errors"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

func sendServiceError(
	ctx tele.Context,
	err error,
) error {
	key, text := serviceErrorNotification(err)
	return core_transport_telegram.Notify(ctx, key, text)
}

func serviceErrorNotification(err error) (string, string) {
	switch {
	case errors.Is(err, core_errors.ErrForbidden):
		return "users.forbidden", "⛔ Недостаточно прав."

	case errors.Is(err, core_errors.ErrNotFound):
		return "users.not_found", "❌ Пользователь не найден."

	case errors.Is(err, core_errors.ErrConflict):
		return "users.conflict", "⚠️ Операция не выполнена: данные уже существуют."

	case errors.Is(err, core_errors.ErrInvalidArgument):
		return "users.invalid_argument", "⚠️ Переданы некорректные данные."

	default:
		return "users.internal", "❌ Не удалось выполнить операцию. Попробуйте позже."
	}
}
