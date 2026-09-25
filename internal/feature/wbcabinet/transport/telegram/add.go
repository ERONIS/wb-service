package wbcabinet_telegram_transport

import (
	"fmt"
	"html"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) startAdd(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil {
		return tele.ErrBadContext
	}
	handler.pending.Begin(
		sender.ID,
		pendingAdd{step: stepCabinetName},
		addCabinetTTL,
	)
	handler.textFlows.BeginTextFlow(sender.ID, addCabinetTextFlow)
	return ctx.EditOrSend(
		"➕ <b>Добавление WB кабинета</b>\n\nВведите понятное название кабинета.",
		addPromptMarkup(),
	)
}

func (handler *Handler) cancelAdd(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil {
		return tele.ErrBadContext
	}
	handler.clearPending(sender.ID)
	return handler.list(ctx)
}

func (handler *Handler) handleText(ctx tele.Context) (bool, error) {
	sender := ctx.Sender()
	if sender == nil {
		return false, nil
	}
	lease, status := handler.pending.Claim(sender.ID)
	if status == core_transport_telegram.StateMissing {
		return false, nil
	}
	_ = ctx.Delete()
	if status == core_transport_telegram.StateBusy {
		return true, core_transport_telegram.Notify(
			ctx,
			"wbcabinet.verification_busy",
			"⏳ Предыдущий токен уже проверяется. Дождитесь результата.",
		)
	}
	if status == core_transport_telegram.StateExpired {
		handler.textFlows.EndTextFlow(sender.ID, addCabinetTextFlow)
		return true, handler.showProfile(
			ctx,
			"⌛ Сессия добавления кабинета истекла. Начните заново.",
		)
	}
	state := lease.Value

	switch state.step {
	case stepCabinetName:
		name := strings.TrimSpace(ctx.Text())
		if name == "" || len([]rune(name)) > wbcabinet_service.MaxCabinetNameLength {
			handler.finishPending(sender.ID, lease, true, state)
			return true, ctx.EditOrSend(
				fmt.Sprintf("⚠️ Название должно содержать от 1 до %d символов.\n\nВведите название кабинета.", wbcabinet_service.MaxCabinetNameLength),
				addPromptMarkup(),
			)
		}
		state.name = name
		state.step = stepCabinetToken
		handler.finishPending(sender.ID, lease, true, state)
		return true, ctx.EditOrSend(
			fmt.Sprintf(
				"Кабинет: <b>%s</b>\n\nОтправьте API-токен с правами Content на чтение и запись. Сообщение с токеном будет удалено.",
				html.EscapeString(name),
			),
			addPromptMarkup(),
		)

	case stepCabinetToken:
		token := strings.TrimSpace(ctx.Text())
		if token == "" {
			handler.finishPending(sender.ID, lease, true, state)
			return true, ctx.EditOrSend("⚠️ Токен не может быть пустым. Отправьте API-токен.", addPromptMarkup())
		}
		if err := ctx.EditOrSend(
			"⏳ Проверяю токен и права Content. Это может занять некоторое время.",
			addPromptMarkup(),
		); err != nil {
			handler.finishPending(sender.ID, lease, true, state)
			return true, err
		}
		cabinet, err := handler.service.Add(handler.ctx, wbcabinet_service.AddCommand{
			OwnerTelegramID: sender.ID,
			Name:            state.name,
			Token:           token,
		})
		token = ""
		if err != nil {
			handler.finishPending(sender.ID, lease, true, state)
			return true, ctx.EditOrSend(serviceErrorText(err), addPromptMarkup())
		}
		handler.finishPending(sender.ID, lease, false, state)
		return true, handler.showProfile(
			ctx,
			fmt.Sprintf("✅ Кабинет <b>%s</b> добавлен.", html.EscapeString(cabinet.Name)),
		)
	default:
		handler.finishPending(sender.ID, lease, false, state)
		return false, nil
	}
}

func (handler *Handler) finishPending(
	telegramID int64,
	lease core_transport_telegram.StateLease[pendingAdd],
	retry bool,
	state pendingAdd,
) {
	lease.Value = state
	if handler.pending.Finish(telegramID, lease, retry, addCabinetTTL) && !retry {
		handler.textFlows.EndTextFlow(telegramID, addCabinetTextFlow)
	}
}

func (handler *Handler) clearPending(telegramID int64) {
	handler.pending.Cancel(telegramID)
	if handler.textFlows != nil {
		handler.textFlows.EndTextFlow(telegramID, addCabinetTextFlow)
	}
}
