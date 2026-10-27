package statistics_telegram_transport

import (
	"fmt"
	"html"

	core_tg_callback "github.com/ERONIS/wb-service/internal/core/transport/telegram/callback"
	core_tg_identity "github.com/ERONIS/wb-service/internal/core/transport/telegram/identity"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	cardpublication_resolution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) listAttention(ctx tele.Context) error {
	filter := statistics_service.AttentionFilter{Limit: 20}
	arguments := callbackArguments(ctx.Args())
	if len(arguments) > 0 {
		transferID, err := parsePositiveInt64(arguments, 0, 1)
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
	rows = append(rows, markup.Row(core_tg_menu.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) openAttention(ctx tele.Context) error {
	item, err := handler.parseAttentionItem(ctx)
	if err != nil {
		return err
	}
	text := fmt.Sprintf(
		"⚠️ <b>Карточка требует решения</b>\n\n"+
			"Артикул: %s\n"+
			"Кабинет: %s\n"+
			"Причина: %s\n\n"+
			"Выберите действие или выполните автоматическую проверку в WB.",
		html.EscapeString(item.VendorCode),
		html.EscapeString(item.SellerID),
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
	rows = append(rows, markup.Row(core_tg_menu.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) markPresent(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_resolution.ManualMarkRemotePresent)
}

func (handler *Handler) markRejected(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_resolution.ManualMarkRejected)
}

func (handler *Handler) closeAttention(ctx tele.Context) error {
	return handler.resolve(ctx, cardpublication_resolution.ManualCloseUnresolvedNoRetry)
}

func (handler *Handler) resolve(
	ctx tele.Context,
	kind cardpublication_resolution.ManualResolutionKind,
) error {
	expectedArguments := 3
	if kind == cardpublication_resolution.ManualCloseUnresolvedNoRetry {
		expectedArguments = 2
	}
	itemTargetID, err := parsePositiveInt64(ctx.Args(), 0, expectedArguments)
	if err != nil {
		return staleCallback(ctx)
	}
	revision, err := core_tg_callback.ParseInt64Argument(
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
	command := cardpublication_resolution.ManualResolutionCommand{
		TransferID:             transfer_model.TransferID(item.TransferID),
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
	case cardpublication_resolution.ManualMarkRemotePresent:
		command.EvidenceObservationID = evidenceID
	case cardpublication_resolution.ManualMarkRejected:
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
		markup.Row(core_tg_menu.MainMenuButton()),
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

func manualActor(ctx tele.Context) (cardpublication_resolution.ManualTrustedActor, error) {
	user, err := core_tg_identity.UserFromContext(ctx)
	if err != nil {
		return cardpublication_resolution.ManualTrustedActor{}, err
	}
	return cardpublication_resolution.ManualTrustedActor{
		ID:          user.TelegramID,
		DisplayName: user.FullName,
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

func resolutionResultText(outcome transfer_model.ResultClass) string {
	switch outcome {
	case transfer_model.ResultSuccess:
		return "Карточка отмечена как присутствующая в WB."
	case transfer_model.ResultRejected:
		return "Карточка отмечена как отклонённая."
	default:
		return "Карточка оставлена без повторной отправки."
	}
}

func (handler *Handler) parseAttentionItem(ctx tele.Context) (statistics_service.AttentionRow, error) {
	itemTargetID, err := parsePositiveInt64(ctx.Args(), 0, 1)
	if err != nil {
		return statistics_service.AttentionRow{}, staleCallback(ctx)
	}
	item, found, err := handler.getAttention(itemTargetID)
	if err != nil {
		return statistics_service.AttentionRow{}, presentError(ctx, err)
	}
	if !found {
		return statistics_service.AttentionRow{}, staleCallback(ctx)
	}
	return item, nil
}

func (handler *Handler) checkAttention(ctx tele.Context) error {
	item, err := handler.parseAttentionItem(ctx)
	if err != nil {
		return err
	}
	if handler.cardVerifier == nil {
		return core_tg_menu.Notify(ctx, "stats.verifier_unavailable", "⚠️ Механизм проверки WB не настроен.")
	}
	info, err := handler.cardVerifier.FindCardByVendorCode(handler.ctx, item.SellerID, item.VendorCode)
	if err != nil {
		return core_tg_menu.Notify(ctx, "stats.verifier_error", fmt.Sprintf("❌ Ошибка при проверке в WB: %v", err))
	}
	if info == nil {
		return core_tg_menu.Notify(ctx, "stats.card_not_found", "Карточка пока не найдена в WB. Она остаётся в списке внимания для дальнейшей проверки.")
	}

	if err := handler.statistics.ResolveVerifiedCard(handler.ctx, item, *info); err != nil {
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
		html.EscapeString(item.SellerID),
	)
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonAttention),
		markup.Row(buttonStatistics),
		markup.Row(core_tg_menu.MainMenuButton()),
	)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) checkAllAttention(ctx tele.Context) error {
	if handler.cardVerifier == nil {
		return core_tg_menu.Notify(ctx, "stats.verifier_unavailable", "⚠️ Механизм проверки WB не настроен.")
	}
	items, err := handler.statistics.ListAttention(handler.ctx, statistics_service.AttentionFilter{Limit: 100})
	if err != nil {
		return presentError(ctx, err)
	}
	if len(items) == 0 {
		return core_tg_menu.Notify(ctx, "stats.all_checked", "✅ Все карточки уже проверены.")
	}

	foundCount := 0
	unresolvedCount := 0
	for _, item := range items {
		info, err := handler.cardVerifier.FindCardByVendorCode(handler.ctx, item.SellerID, item.VendorCode)
		if err != nil {
			unresolvedCount++
			continue
		}
		if info != nil {
			if err := handler.statistics.ResolveVerifiedCard(handler.ctx, item, *info); err != nil {
				unresolvedCount++
				continue
			}
			foundCount++
		} else {
			unresolvedCount++
		}
	}

	text := fmt.Sprintf(
		"🔍 <b>Проверка карточек в WB завершена</b>\n\n"+
			"✅ Найдено и подтверждено в WB: <b>%d</b>\n"+
			"⚠️ Остались для дальнейшей проверки: <b>%d</b>\n\n"+
			"Неподтверждённые карточки остаются в списке внимания.",
		foundCount,
		unresolvedCount,
	)
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonAttention),
		markup.Row(buttonStatistics),
		markup.Row(core_tg_menu.MainMenuButton()),
	)
	return ctx.EditOrSend(text, markup)
}
