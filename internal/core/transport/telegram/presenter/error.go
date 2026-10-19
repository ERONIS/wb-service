package core_tg_presenter

import (
	"errors"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"

	tele "gopkg.in/telebot.v3"
)

type ErrorCase struct {
	Target error
	Key    string
	Text   string
}

func OnError(target error, key, text string) ErrorCase {
	return ErrorCase{Target: target, Key: key, Text: text}
}

func DefaultCoreErrorCases() []ErrorCase {
	return []ErrorCase{
		OnError(core_errors.ErrUnauthorized, "common.unauthorized", "🔒 Требуется авторизация."),
		OnError(core_errors.ErrForbidden, "common.forbidden", "⛔ Недостаточно прав."),
		OnError(core_errors.ErrNotFound, "common.not_found", "❌ Запрошенные данные не найдены."),
		OnError(core_errors.ErrConflict, "common.conflict", "⚠️ Данные уже существуют или были изменены."),
		OnError(core_errors.ErrInvalidArgument, "common.invalid_argument", "⚠️ Переданы некорректные данные."),
		OnError(core_errors.ErrRateLimited, "common.rate_limited", "⏳ Слишком много запросов. Попробуйте позже."),
		OnError(core_errors.ErrQueueFull, "common.queue_full", "⚠️ Очередь запросов заполнена. Попробуйте позже."),
		OnError(core_errors.ErrCapacityExceeded, "common.capacity_exceeded", "⚠️ Превышен допустимый объем данных."),
		OnError(core_errors.ErrPayloadTooLarge, "common.payload_too_large", "⚠️ Размер переданных данных превышает допустимый лимит."),
		OnError(core_errors.ErrUnavailable, "common.unavailable", "🔌 Сервис временно недоступен. Попробуйте позже."),
		OnError(core_errors.ErrTimeout, "common.timeout", "⏱️ Время ожидания ответа истекло. Попробуйте позже."),
	}
}

func MatchError(
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) ErrorCase {
	for _, item := range cases {
		if item.Target != nil && errors.Is(err, item.Target) {
			return item
		}
	}
	return fallback
}

func MatchErrorWithDefaults(
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) ErrorCase {
	for _, item := range cases {
		if item.Target != nil && errors.Is(err, item.Target) {
			return item
		}
	}
	for _, item := range DefaultCoreErrorCases() {
		if item.Target != nil && errors.Is(err, item.Target) {
			return item
		}
	}
	return fallback
}

func NotifyError(
	ctx tele.Context,
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) error {
	notification := MatchError(err, fallback, cases...)
	return core_tg_menu.Notify(ctx, notification.Key, notification.Text)
}

func NotifyErrorWithDefaults(
	ctx tele.Context,
	err error,
	fallback ErrorCase,
	cases ...ErrorCase,
) error {
	notification := MatchErrorWithDefaults(err, fallback, cases...)
	return core_tg_menu.Notify(ctx, notification.Key, notification.Text)
}

// DefaultFallbackErrorCase задаёт стандартное уведомление об ошибке.
var DefaultFallbackErrorCase = OnError(
	nil,
	"common.internal",
	"❌ Не удалось выполнить операцию. Попробуйте позже.",
)

// NotifyServiceError показывает ошибку пользователю с учётом дополнительных и стандартных вариантов.
func NotifyServiceError(
	ctx tele.Context,
	err error,
	extraCases ...ErrorCase,
) error {
	return NotifyErrorWithDefaults(ctx, err, DefaultFallbackErrorCase, extraCases...)
}
