package statistics_telegram_transport

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) listAttention(ctx tele.Context) error {
	filter := statistics_service.AttentionFilter{Limit: 20}
	if len(ctx.Args()) > 0 {
		transferID, err := parsePositiveInt64(ctx.Args(), 0, 1)
		if err != nil {
			return staleCallback(ctx)
		}
		filter.TransferID = transferID
	}
	items, err := handler.statistics.ListAttention(handler.ctx, filter)
	if err != nil {
		return presentError(ctx, err)
	}
	text := "⚠️ <b>Требуют внимания</b>\n\n"
	if len(items) == 0 {
		text += "Все карточки проверены."
	} else {
		text += "Эти карточки не удалось проверить автоматически. Выберите карточку, чтобы принять решение."
	}
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(items)+2)
	for _, item := range items {
		rows = append(rows, markup.Row(markup.Data(
			fmt.Sprintf("%s · %s", item.VendorCode, attentionReason(item.OutcomeCode)),
			buttonAttentionItem.Unique,
			callbackInt(item.ItemTargetID),
		)))
	}
	rows = append(rows, markup.Row(buttonStatistics))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) openAttention(ctx tele.Context) error {
	itemTargetID, err := parsePositiveInt64(ctx.Args(), 0, 1)
	if err != nil {
		return staleCallback(ctx)
	}
	item, found, err := handler.getAttention(itemTargetID)
	if err != nil {
		return presentError(ctx, err)
	}
	if !found {
		return staleCallback(ctx)
	}
	text := fmt.Sprintf(
		"⚠️ <b>Карточка требует решения</b>\n\n"+
			"Артикул: <code>%s</code>\n"+
			"Кабинет: <code>%s</code>\n"+
			"Причина: %s\n\n"+
			"Выберите только тот результат, который вы проверили в кабинете WB.",
		html.EscapeString(item.VendorCode),
		html.EscapeString(item.CabinetID),
		html.EscapeString(attentionReason(item.OutcomeCode)),
	)
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, 5)
	if item.ObservationEvidenceID > 0 {
		rows = append(rows, markup.Row(markup.Data(
			buttonMarkPresent.Text,
			buttonMarkPresent.Unique,
			callbackInt(item.ItemTargetID),
			callbackInt(item.ActionRevision),
			callbackInt(item.ObservationEvidenceID),
		)))
	}
	if item.ErrorBatchEvidenceID > 0 {
		rows = append(rows, markup.Row(markup.Data(
			buttonMarkRejected.Text,
			buttonMarkRejected.Unique,
			callbackInt(item.ItemTargetID),
			callbackInt(item.ActionRevision),
			callbackInt(item.ErrorBatchEvidenceID),
		)))
	}
	rows = append(rows, markup.Row(markup.Data(
		buttonCloseAttention.Text,
		buttonCloseAttention.Unique,
		callbackInt(item.ItemTargetID),
		callbackInt(item.ActionRevision),
	)))
	rows = append(rows, markup.Row(buttonAttention))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) markPresent(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_service.ManualMarkRemotePresent)
}

func (handler *Handler) markRejected(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_service.ManualMarkRejected)
}

func (handler *Handler) closeAttention(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_service.ManualCloseUnresolvedNoRetry)
}

func (handler *Handler) resolve(
	ctx tele.Context,
	kind cardpublication_service.ManualResolutionKind,
) error {
	expectedArguments := 3
	if kind == cardpublication_service.ManualCloseUnresolvedNoRetry {
		expectedArguments = 2
	}
	itemTargetID, err := parsePositiveInt64(ctx.Args(), 0, expectedArguments)
	if err != nil {
		return staleCallback(ctx)
	}
	revision, err := parseNonNegativeInt64(ctx.Args(), 1, expectedArguments)
	if err != nil {
		return staleCallback(ctx)
	}
	evidenceID := int64(0)
	if expectedArguments == 3 {
		evidenceID, err = parsePositiveInt64(ctx.Args(), 2, expectedArguments)
		if err != nil {
			return staleCallback(ctx)
		}
	}
	item, found, err := handler.getAttention(itemTargetID)
	if err != nil {
		return presentError(ctx, err)
	}
	if !found || item.ActionRevision != revision {
		return staleCallback(ctx)
	}
	actor, err := manualActor(ctx)
	if err != nil {
		return presentError(ctx, err)
	}
	command := cardpublication_service.ManualResolutionCommand{
		TransferID:             transfer_service.TransferID(item.TransferID),
		ActionID:               item.ActionID,
		ActionMemberID:         item.ActionMemberID,
		ExpectedActionRevision: revision,
		Kind:                   kind,
		IdempotencyKey: fmt.Sprintf(
			"telegram:manual:%s:%d:%d:%d",
			kind, itemTargetID, revision, evidenceID,
		),
	}
	switch kind {
	case cardpublication_service.ManualMarkRemotePresent:
		command.EvidenceObservationID = evidenceID
	case cardpublication_service.ManualMarkRejected:
		command.EvidenceErrorBatchID = evidenceID
	}
	resolution, err := handler.manualResolver.Resolve(handler.ctx, actor, command)
	if err != nil {
		return presentError(ctx, err)
	}
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonAttention),
		markup.Row(buttonStatistics),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(fmt.Sprintf(
		"✅ Решение сохранено.\n\n%s Повторная отправка в WB не выполнялась.",
		resolutionResultText(resolution.OutcomeClass),
	), markup)
}

func (handler *Handler) getAttention(itemTargetID int64) (statistics_service.AttentionRow, bool, error) {
	items, err := handler.statistics.ListAttention(handler.ctx, statistics_service.AttentionFilter{
		ItemTargetID: itemTargetID,
		Limit:        1,
	})
	if err != nil || len(items) == 0 {
		return statistics_service.AttentionRow{}, false, err
	}
	return items[0], true, nil
}

func manualActor(ctx tele.Context) (cardpublication_service.ManualTrustedActor, error) {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return cardpublication_service.ManualTrustedActor{}, errors.New("Telegram actor is unavailable")
	}
	name := strings.TrimSpace(strings.Join([]string{sender.FirstName, sender.LastName}, " "))
	if name == "" {
		name = strings.TrimSpace(sender.Username)
	}
	if name == "" {
		name = fmt.Sprintf("Telegram %d", sender.ID)
	}
	runes := []rune(name)
	if len(runes) > 100 {
		name = string(runes[:100])
	}
	return cardpublication_service.ManualTrustedActor{ID: sender.ID, DisplayName: name}, nil
}

func parseNonNegativeInt64(arguments []string, index, expected int) (int64, error) {
	if len(arguments) != expected || index < 0 || index >= len(arguments) {
		return 0, errors.New("invalid callback arguments")
	}
	value, err := strconv.ParseInt(arguments[index], 36, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid callback revision")
	}
	return value, nil
}

func attentionReason(code string) string {
	switch code {
	case "UNKNOWN_DELIVERY", "UNRESOLVED_AFTER_RECONCILIATION":
		return "WB не подтвердил результат отправки"
	case "INTERNAL_ERROR":
		return "не удалось завершить автоматическую проверку"
	default:
		return "нужно проверить результат в WB"
	}
}

func resolutionResultText(outcome transfer_service.ResultClass) string {
	switch outcome {
	case transfer_service.ResultSuccess:
		return "Карточка отмечена как присутствующая в WB."
	case transfer_service.ResultRejected:
		return "Карточка отмечена как отклонённая."
	default:
		return "Карточка оставлена без повторной отправки."
	}
}
