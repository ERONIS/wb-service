package statistics_telegram_transport

import (
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	tele "gopkg.in/telebot.v3"
)

func presentError(ctx tele.Context, err error) error {
	return core_transport_telegram.NotifyServiceError(ctx, err)
}
