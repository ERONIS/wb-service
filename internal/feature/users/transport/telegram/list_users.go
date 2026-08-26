package users_transport_tg

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"

	tele "gopkg.in/telebot.v3"
)

func (h *UsersTgHandler) ListUsers(
	ctx tele.Context,
) error {
	page, err := parseUsersPage(ctx)
	if err != nil {
		return core_transport_telegram.Notify(ctx, "users.invalid_page", "Не удалось определить страницу списка.")
	}

	result, err := h.usersService.GetUsers(
		h.ctx,
		ctx.Sender().ID,
		page,
	)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return ctx.EditOrSend(
		usersPageText(result),
		usersPageMarkup(result),
	)
}

func parseUsersPage(ctx tele.Context) (int, error) {
	return parsePageArguments(ctx.Args())
}

func parsePageArguments(arguments []string) (int, error) {
	if len(arguments) == 0 {
		return 1, nil
	}
	if len(arguments) != 1 {
		return 0, fmt.Errorf(
			"unexpected arguments count: %d",
			len(arguments),
		)
	}

	return parsePageNumber(arguments[0])
}

func parsePageNumber(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1, nil
	}

	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return 0, fmt.Errorf("invalid page %q", value)
	}

	return page, nil
}

func usersPageText(result users_service.UsersPage) string {
	if len(result.Users) == 0 {
		if result.Page == 1 {
			return "👥 Пользователей пока нет."
		}

		return "На этой странице нет пользователей."
	}

	var message strings.Builder

	fmt.Fprintf(
		&message,
		"👥 <b>Пользователи</b>\n"+
			"Страница: %d\n\n",
		result.Page,
	)

	for index, user := range result.Users {
		fmt.Fprintf(
			&message,
			"%d. <b>%s</b>\n"+
				"TG ID: <code>%d</code>\n"+
				"Роль: %s\n\n",
			index+1,
			html.EscapeString(user.FullName),
			user.TelegramID,
			user.Role,
		)
	}

	return strings.TrimSpace(message.String())
}

func usersPageMarkup(
	result users_service.UsersPage,
) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := make([]tele.Row, 0, len(result.Users)+3)
	navigation := make([]tele.Btn, 0, 2)

	rows = append(
		rows,
		markup.Row(
			markup.Data(
				buttonStartAddUser.Text,
				buttonStartAddUser.Unique,
				strconv.Itoa(result.Page),
			),
		),
	)

	for index, user := range result.Users {
		rows = append(
			rows,
			markup.Row(
				markup.Data(
					fmt.Sprintf(
						"👤 %d. %s",
						index+1,
						user.FullName,
					),
					buttonGetUser.Unique,
					strconv.FormatInt(user.TelegramID, 10),
					strconv.Itoa(result.Page),
				),
			),
		)
	}

	if result.Page > 1 {
		navigation = append(
			navigation,
			markup.Data(
				"⬅️ Назад",
				buttonListUsers.Unique,
				strconv.Itoa(result.Page-1),
			),
		)
	}

	if result.HasNext {
		navigation = append(
			navigation,
			markup.Data(
				"Далее ➡️",
				buttonListUsers.Unique,
				strconv.Itoa(result.Page+1),
			),
		)
	}

	if len(navigation) > 0 {
		rows = append(
			rows,
			markup.Row(navigation...),
		)
	}

	rows = append(rows, mainMenuRow(markup))

	markup.Inline(rows...)

	return markup
}
