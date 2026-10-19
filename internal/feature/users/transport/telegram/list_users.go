package users_transport_tg

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	core_tg_callback "github.com/ERONIS/wb-service/internal/core/transport/telegram/callback"
	core_tg_identity "github.com/ERONIS/wb-service/internal/core/transport/telegram/identity"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	core_tg_ui "github.com/ERONIS/wb-service/internal/core/transport/telegram/ui"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"

	tele "gopkg.in/telebot.v3"
)

func (h *UsersTgHandler) ListUsers(
	ctx tele.Context,
) error {
	page, err := parseUsersPage(ctx)
	if err != nil {
		return core_tg_menu.Notify(ctx, "users.invalid_page", "Не удалось определить страницу списка.")
	}
	adminTelegramID, err := core_tg_identity.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	result, err := h.usersService.GetUsers(
		h.ctx,
		adminTelegramID,
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
	args := ctx.Args()
	if len(args) == 0 {
		return 1, nil
	}
	if len(args) != 1 {
		return 0, fmt.Errorf("unexpected arguments count: %d", len(args))
	}
	return core_tg_callback.ParsePageNumber(args[0])
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
				"TG ID: %d\n"+
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

	totalPages := result.Page
	if result.HasNext {
		totalPages = result.Page + 1
	}
	if navRow, ok := core_tg_ui.BuildPaginationRow(markup, core_tg_ui.PaginationConfig{
		Page:           result.Page,
		TotalPages:     totalPages,
		CallbackUnique: buttonListUsers.Unique,
		NextText:       "Далее ➡️",
	}); ok {
		rows = append(rows, navRow)
	}

	rows = append(rows, mainMenuRow(markup))

	markup.Inline(rows...)

	return markup
}
