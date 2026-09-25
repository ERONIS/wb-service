package users_transport_tg

import (
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

func sendServiceError(ctx tele.Context, err error) error {
	return core_transport_telegram.NotifyServiceError(ctx, err)
}
