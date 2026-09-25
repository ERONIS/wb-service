package statistics_telegram_transport

import (
	"fmt"
	"html"

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
	rows := make([]tele.Row, 0, len(items)+3)
	for _, item := range items {
		rows = append(rows, markup.Row(markup.Data(
			fmt.Sprintf("%s · %s", item.VendorCode, attentionReason(item.OutcomeCode)),
			buttonAttentionItem.Unique,
			callbackInt(item.ItemTargetID),
		)))
	}
	if len(items) > 0 && handler.cardVerifier != nil {
		rows = append(rows, markup.Row(buttonCheckAllInWB))
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
			"Артикул: %s\n"+
			"Кабинет: %s\n"+
			"Причина: %s\n\n"+
			"Выберите действие или выполните автоматическую проверку в WB.",
		html.EscapeString(item.VendorCode),
		html.EscapeString(item.CabinetID),
		html.EscapeString(attentionReason(item.OutcomeCode)),
	)
	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, 6)
	if handler.cardVerifier != nil {
		rows = append(rows, markup.Row(markup.Data(
			buttonCheckInWB.Text,
			buttonCheckInWB.Unique,
			callbackInt(item.ItemTargetID),
		)))
	}
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
	revision, err := core_transport_telegram.ParseInt64Argument(
		ctx.Args(),
		1,
		expectedArguments,
		36,
		0,
	)
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
	actor, err := core_transport_telegram.ActorFromContext(ctx)
	if err != nil {
		return cardpublication_service.ManualTrustedActor{}, err
	}
	return cardpublication_service.ManualTrustedActor{
		ID:          actor.TelegramUserID,
		DisplayName: actor.DisplayName,
	}, nil
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

func (handler *Handler) checkAttention(ctx tele.Context) error {
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
	if handler.cardVerifier == nil {
		return ctx.Send("Механизм проверки WB не настроен.")
	}
	info, err := handler.cardVerifier.FindCardByVendorCode(handler.ctx, item.CabinetID, item.VendorCode)
	if err != nil {
		return ctx.Send(fmt.Sprintf("Ошибка при проверке в WB: %v", err))
	}
	if info == nil {
		if err := handler.statistics.RequeueCardForCreation(handler.ctx, item.ItemTargetID); err != nil {
			return presentError(ctx, err)
		}
		text := fmt.Sprintf(
			"🔄 <b>Карточка не найдена в WB и отправлена на создание!</b>\n\n"+
				"Артикул: %s\n"+
				"Кабинет: %s\n\n"+
				"Карточка возвращена в очередь на создание в Wildberries.",
			html.EscapeString(item.VendorCode),
			html.EscapeString(item.CabinetID),
		)
		markup := handler.bot.NewMarkup()
		markup.Inline(
			markup.Row(buttonAttention),
			markup.Row(buttonStatistics),
			markup.Row(core_transport_telegram.MainMenuButton()),
		)
		return ctx.EditOrSend(text, markup)
	}

	if err := handler.statistics.ResolveVerifiedCard(handler.ctx, item.ItemTargetID, info.NMID, info.IMTID, info.SubjectID); err != nil {
		return presentError(ctx, err)
	}

	text := fmt.Sprintf(
		"✅ <b>Карточка найдена и подтверждена в WB!</b>\n\n"+
			"Артикул: %s\n"+
			"WB Артикул (nmID): <code>%d</code>\n"+
			"Название: %s\n"+
			"Кабинет: %s\n\n"+
			"Карточка успешно отмечена как созданная и убрана из списка внимания.",
		html.EscapeString(item.VendorCode),
		info.NMID,
		html.EscapeString(info.Title),
		html.EscapeString(item.CabinetID),
	)
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonAttention),
		markup.Row(buttonStatistics),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) checkAllAttention(ctx tele.Context) error {
	if handler.cardVerifier == nil {
		return ctx.Send("Механизм проверки WB не настроен.")
	}
	items, err := handler.statistics.ListAttention(handler.ctx, statistics_service.AttentionFilter{Limit: 100})
	if err != nil {
		return presentError(ctx, err)
	}
	if len(items) == 0 {
		return ctx.Send("Все карточки уже проверены.")
	}

	foundCount := 0
	requeuedCount := 0
	for _, item := range items {
		info, err := handler.cardVerifier.FindCardByVendorCode(handler.ctx, item.CabinetID, item.VendorCode)
		if err != nil {
			continue
		}
		if info != nil {
			if err := handler.statistics.ResolveVerifiedCard(handler.ctx, item.ItemTargetID, info.NMID, info.IMTID, info.SubjectID); err != nil {
				continue
			}
			foundCount++
		} else {
			if err := handler.statistics.RequeueCardForCreation(handler.ctx, item.ItemTargetID); err != nil {
				continue
			}
			requeuedCount++
		}
	}

	text := fmt.Sprintf(
		"🔍 <b>Проверка карточек в WB завершена</b>\n\n"+
			"✅ Найдено и подтверждено в WB: <b>%d</b>\n"+
			"🔄 Не найдено в WB и отправлено на создание: <b>%d</b>\n\n"+
			"Все карточки успешно обработаны и убраны из списка внимания.",
		foundCount,
		requeuedCount,
	)
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonAttention),
		markup.Row(buttonStatistics),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return ctx.EditOrSend(text, markup)
}

