package users_transport_tg

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

const userCardTimeFormat = "02.01.2006 15:04"

func (h *UsersTgHandler) getUser(
	ctx tele.Context,
	targetTelegramID int64,
	page int,
) error {
	adminTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	user, err := h.usersService.GetUser(
		h.ctx,
		adminTelegramID,
		targetTelegramID,
	)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return ctx.EditOrSend(
		userCardText(user),
		userCardMarkup(user, page),
	)
}

func userCardText(user domain.User) string {
	nickname := "не указан"
	if user.TelegramNickname != nil {
		nickname = html.EscapeString(
			strings.TrimSpace(*user.TelegramNickname),
		)
	}

	return fmt.Sprintf(
		"👤 <b>Пользователь</b>\n\n"+
			"Имя: <b>%s</b>\n"+
			"TG ID: %d\n"+
			"Nickname: %s\n"+
			"Роль: %s\n"+
			"Создан: %s\n"+
			"Обновлён: %s",
		html.EscapeString(user.FullName),
		user.TelegramID,
		nickname,
		user.Role,
		formatUserCardTime(user.CreatedAt),
		formatUserCardTime(user.UpdatedAt),
	)
}

func userCardMarkup(
	user domain.User,
	page int,
) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, 6)
	payload := userPayload(user.TelegramID, page)

	if !user.IsAdmin() {
		if user.IsPartner() {
			rows = append(rows, markup.Row(
				markup.Data(
					buttonRevokePartner.Text,
					buttonRevokePartner.Unique,
					payload...,
				),
			))
		} else {
			rows = append(rows, markup.Row(
				markup.Data(
					buttonSetPartner.Text,
					buttonSetPartner.Unique,
					payload...,
				),
			))
		}
		rows = append(
			rows,
			markup.Row(
				markup.Data(
					buttonSetAdmin.Text,
					buttonSetAdmin.Unique,
					payload...,
				),
			),
			markup.Row(
				markup.Data(
					buttonConfirmDeleteUser.Text,
					buttonConfirmDeleteUser.Unique,
					payload...,
				),
			),
		)
	}

	rows = append(rows, usersNavigationRows(markup, page)...)

	markup.Inline(rows...)

	return markup
}

func formatUserCardTime(value time.Time) string {
	if value.IsZero() {
		return "не указано"
	}

	return value.UTC().Format(userCardTimeFormat) + " UTC"
}
