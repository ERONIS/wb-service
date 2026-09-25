package users_transport_tg

import (
	"fmt"
	"strconv"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

const addUserPromptText = "➕ <b>Добавление пользователя</b>\n\n" +
	"Отправьте TG ID и полное имя одной строкой.\n\n" +
	"Пример: 123456789 Иван Иванов"

func (h *UsersTgHandler) StartAddUser(ctx tele.Context) error {
	page, err := parseUsersPage(ctx)
	if err != nil {
		return core_transport_telegram.Notify(ctx, "users.invalid_page", "Не удалось определить страницу списка.")
	}
	senderID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	h.pendingAdds.Begin(senderID, pendingAddState{page: page}, pendingAddTTL)
	h.textFlows.BeginTextFlow(senderID, addUserTextFlow)

	return ctx.EditOrSend(
		addUserPromptText,
		addUserPromptMarkup(page),
	)
}

func (h *UsersTgHandler) CancelAddUser(ctx tele.Context) error {
	senderID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	h.clearPendingAdd(senderID)

	return h.ListUsers(ctx)
}

func (h *UsersTgHandler) createUser(
	ctx tele.Context,
	adminTelegramID int64,
	targetTelegramID int64,
	fullName string,
	page int,
) (bool, error) {
	user, err := h.usersService.AddUser(
		h.ctx,
		adminTelegramID,
		targetTelegramID,
		fullName,
	)
	if err != nil {
		return false, sendServiceError(ctx, err)
	}

	return true, ctx.Send(
		"✅ Пользователь добавлен.\n\n"+userCardText(user),
		userCardMarkup(user, page),
	)
}

func parseAddUserInput(arguments []string) (int64, string, error) {
	if len(arguments) < 2 {
		return 0, "", fmt.Errorf("not enough arguments")
	}

	telegramID, err := core_transport_telegram.ParseTelegramID(arguments[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid Telegram ID %q", arguments[0])
	}

	return telegramID, strings.Join(arguments[1:], " "), nil
}

func addUserPromptMarkup(page int) *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(
			markup.Data(
				buttonCancelAddUser.Text,
				buttonCancelAddUser.Unique,
				strconv.Itoa(page),
			),
		),
	)

	return markup
}

func (h *UsersTgHandler) finishPendingAdd(
	telegramID int64,
	pending core_transport_telegram.StateLease[pendingAddState],
	retry bool,
) {
	if h.pendingAdds.Finish(telegramID, pending, retry, pendingAddTTL) &&
		!retry && h.textFlows != nil {
		h.textFlows.EndTextFlow(telegramID, addUserTextFlow)
	}
}

func (h *UsersTgHandler) clearPendingAdd(telegramID int64) {
	h.pendingAdds.Cancel(telegramID)
	if h.textFlows != nil {
		h.textFlows.EndTextFlow(telegramID, addUserTextFlow)
	}
}
