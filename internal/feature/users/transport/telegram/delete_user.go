package users_transport_tg

import (
	"fmt"

	tele "gopkg.in/telebot.v3"
)

func (h *UsersTgHandler) deleteUser(
	ctx tele.Context,
	targetTelegramID int64,
	opts ...interface{},
) error {
	if err := h.usersService.DeleteUser(
		h.ctx,
		ctx.Sender().ID,
		targetTelegramID,
	); err != nil {
		return sendServiceError(ctx, err)
	}

	return ctx.EditOrSend(
		fmt.Sprintf(
			"✅ Пользователь <code>%d</code> удалён.",
			targetTelegramID,
		),
		opts...,
	)
}
