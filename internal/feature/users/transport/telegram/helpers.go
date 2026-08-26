package users_transport_tg

import (
	"fmt"
	"strconv"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

type userCallbackHandler func(
	ctx tele.Context,
	telegramID int64,
	page int,
) error

func withUserPayload(next userCallbackHandler) tele.HandlerFunc {
	return func(ctx tele.Context) error {
		telegramID, page, err := parseUserCallbackPayload(ctx.Args())
		if err != nil {
			return core_transport_telegram.Notify(
				ctx,
				"users.invalid_callback",
				"Не удалось определить выбранного пользователя.",
			)
		}

		return next(ctx, telegramID, page)
	}
}

func parseUserCallbackPayload(
	arguments []string,
) (telegramID int64, page int, err error) {
	if len(arguments) != 2 {
		return 0, 0, fmt.Errorf(
			"unexpected callback arguments count: %d",
			len(arguments),
		)
	}

	telegramID, err = core_transport_telegram.ParseTelegramID(arguments[0])
	if err != nil {
		return 0, 0, err
	}

	page, err = parsePageNumber(arguments[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse users page: %w", err)
	}

	return telegramID, page, nil
}

func userPayload(telegramID int64, page int) []string {
	return []string{
		strconv.FormatInt(telegramID, 10),
		strconv.Itoa(page),
	}
}

func usersNavigationRows(
	markup *tele.ReplyMarkup,
	page int,
) []tele.Row {
	return []tele.Row{
		markup.Row(
			markup.Data(
				"⬅️ К списку",
				buttonListUsers.Unique,
				strconv.Itoa(page),
			),
		),
		mainMenuRow(markup),
	}
}

func mainMenuRow(markup *tele.ReplyMarkup) tele.Row {
	return markup.Row(core_transport_telegram.MainMenuButton())
}
