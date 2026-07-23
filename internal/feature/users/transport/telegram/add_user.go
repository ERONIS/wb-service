package users_transport_tg

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"

	tele "gopkg.in/telebot.v3"
)

const addUserPromptText = "➕ <b>Добавление пользователя</b>\n\n" +
	"Отправьте TG ID и полное имя одной строкой.\n\n" +
	"Пример: <code>123456789 Иван Иванов</code>"

func (h *UsersTgHandler) StartAddUser(ctx tele.Context) error {
	page, err := parseUsersPage(ctx)
	if err != nil {
		return ctx.EditOrSend("Не удалось определить страницу списка.")
	}

	h.setPendingAdd(ctx.Sender().ID, page)

	return ctx.EditOrSend(
		addUserPromptText,
		addUserPromptMarkup(page),
	)
}

func (h *UsersTgHandler) AddUserInput(
	ctx tele.Context,
) error {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return nil
	}

	pending, claimed := h.claimPendingAdd(sender.ID)
	if !claimed {
		return nil
	}

	targetTelegramID, fullName, err := parseAddUserInput(
		strings.Fields(ctx.Text()),
	)
	if err != nil {
		h.finishPendingAdd(sender.ID, pending.id, true)
		return ctx.Send(
			"⚠️ Неверный формат.\n\n"+addUserPromptText,
			addUserPromptMarkup(pending.page),
		)
	}

	created, err := h.createUser(
		ctx,
		sender.ID,
		targetTelegramID,
		fullName,
		pending.page,
	)
	h.finishPendingAdd(sender.ID, pending.id, !created)

	return err
}

func (h *UsersTgHandler) CancelAddUser(ctx tele.Context) error {
	h.clearPendingAdd(ctx.Sender().ID)

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

func (h *UsersTgHandler) setPendingAdd(telegramID int64, page int) {
	h.pendingAddMu.Lock()
	defer h.pendingAddMu.Unlock()

	h.nextPendingAddID++
	h.pendingAdds[telegramID] = pendingAddState{
		id:        h.nextPendingAddID,
		page:      page,
		expiresAt: time.Now().Add(pendingAddTTL),
	}
}

func (h *UsersTgHandler) claimPendingAdd(
	telegramID int64,
) (pendingAddState, bool) {
	h.pendingAddMu.Lock()
	defer h.pendingAddMu.Unlock()

	state, ok := h.pendingAdds[telegramID]
	if !ok {
		return pendingAddState{}, false
	}
	if state.busy || time.Now().After(state.expiresAt) {
		if !state.busy {
			delete(h.pendingAdds, telegramID)
		}
		return pendingAddState{}, false
	}

	state.busy = true
	h.pendingAdds[telegramID] = state

	return state, true
}

func (h *UsersTgHandler) finishPendingAdd(
	telegramID int64,
	pendingID uint64,
	retry bool,
) {
	h.pendingAddMu.Lock()
	defer h.pendingAddMu.Unlock()

	state, ok := h.pendingAdds[telegramID]
	if !ok || state.id != pendingID {
		return
	}
	if !retry {
		delete(h.pendingAdds, telegramID)
		return
	}

	state.busy = false
	state.expiresAt = time.Now().Add(pendingAddTTL)
	h.pendingAdds[telegramID] = state
}

func (h *UsersTgHandler) clearPendingAdd(telegramID int64) {
	h.pendingAddMu.Lock()
	defer h.pendingAddMu.Unlock()

	delete(h.pendingAdds, telegramID)
}
