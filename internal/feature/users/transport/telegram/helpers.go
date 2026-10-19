package users_transport_tg

import (
	"fmt"
	core_tg_identity "github.com/ERONIS/wb-service/internal/core/transport/telegram/identity"
	"strconv"

	core_tg_callback "github.com/ERONIS/wb-service/internal/core/transport/telegram/callback"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	core_tg_presenter "github.com/ERONIS/wb-service/internal/core/transport/telegram/presenter"

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
			return core_tg_menu.Notify(
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

	telegramID, err = core_tg_identity.ParseTelegramID(arguments[0])
	if err != nil {
		return 0, 0, err
	}

	page, err = core_tg_callback.ParsePageNumber(arguments[1])
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
	return markup.Row(core_tg_menu.MainMenuButton())
}

func sendServiceError(ctx tele.Context, err error) error {
	return core_tg_presenter.NotifyServiceError(ctx, err)
}
