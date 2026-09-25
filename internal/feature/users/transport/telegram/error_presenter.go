package users_transport_tg

import (
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

func sendServiceError(
	ctx tele.Context,
	err error,
) error {
	return core_transport_telegram.NotifyError(
		ctx,
		err,
		core_transport_telegram.OnError(nil, "users.internal", "❌ Не удалось выполнить операцию. Попробуйте позже."),
		core_transport_telegram.OnError(core_errors.ErrForbidden, "users.forbidden", "⛔ Недостаточно прав."),
		core_transport_telegram.OnError(core_errors.ErrNotFound, "users.not_found", "❌ Пользователь не найден."),
		core_transport_telegram.OnError(core_errors.ErrConflict, "users.conflict", "⚠️ Операция не выполнена: данные уже существуют."),
		core_transport_telegram.OnError(core_errors.ErrInvalidArgument, "users.invalid_argument", "⚠️ Переданы некорректные данные."),
	)
}
