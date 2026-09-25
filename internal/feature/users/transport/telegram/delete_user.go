package users_transport_tg

import (
	"fmt"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

func (h *UsersTgHandler) deleteUser(
	ctx tele.Context,
	targetTelegramID int64,
	opts ...interface{},
) error {
	adminTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	if err := h.usersService.DeleteUser(
		h.ctx,
		adminTelegramID,
		targetTelegramID,
	); err != nil {
		return sendServiceError(ctx, err)
	}

	return ctx.EditOrSend(
		fmt.Sprintf(
			"✅ Пользователь %d удалён.",
			targetTelegramID,
		),
		opts...,
	)
}
