package statistics_telegram_transport

import (
	"fmt"
	"strconv"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

const dashboardOperationsLimit = 10

func (handler *Handler) showDashboard(ctx tele.Context) error {
	transfers, err := handler.statistics.AggregateTransfers(handler.ctx, statistics_service.AggregateFilter{})
	if err != nil {
		return presentError(ctx, err)
	}
	operations, err := handler.statistics.ListOperations(handler.ctx, statistics_service.OperationFilter{
		Limit: dashboardOperationsLimit,
	})
	if err != nil {
		return presentError(ctx, err)
	}
	var pendingBatch *cardimport_service.BatchHeader
	arguments := callbackArguments(ctx.Args())
	if len(arguments) > 0 {
		batchID, parseErr := parsePositiveInt64(arguments, 0, 1)
		if parseErr != nil {
			return staleCallback(ctx)
		}
		currentOperations, currentErr := handler.statistics.ListOperations(
			handler.ctx,
			statistics_service.OperationFilter{BatchID: batchID, Limit: 1},
		)
		if currentErr != nil {
			return presentError(ctx, currentErr)
		}
		found := false
		for _, operation := range operations {
			if operation.BatchID == batchID {
				found = true
				break
			}
		}
		if len(currentOperations) > 0 && !found {
			operations = append(currentOperations, operations...)
			found = true
		}
		if !found && len(currentOperations) == 0 {
			batch, batchErr := handler.batches.GetBatch(
				handler.ctx,
				cardimport_service.BatchID(batchID),
			)
			if batchErr != nil {
				return presentError(ctx, batchErr)
			}
			pendingBatch = &batch
		}
	}

	inProgress := transfers.Total - transfers.Terminal
	if inProgress < 0 {
		inProgress = 0
	}
	sessionsTotal := transfers.Total
	if pendingBatch != nil {
		sessionsTotal++
		inProgress++
	}
	text := fmt.Sprintf(
		"📊 <b>Статистика</b>\n\n"+
			"🗂️ Сеансов: <b>%d</b>\n"+
			"🔵 В работе: %d\n"+
			"🟢 Завершено: %d\n\n"+
			"🕘 <b>Последние сеансы</b>",
		sessionsTotal, inProgress, transfers.Terminal,
	)
	if len(operations) == 0 && pendingBatch == nil {
		text += "\nСеансов пока нет."
	}

	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(operations)+2)
	if pendingBatch != nil {
		rows = append(rows, markup.Row(markup.Data(
			fmt.Sprintf("⏳ Сеанс №%d · Проверяем карточки", pendingBatch.SourceSessionID),
			buttonCurrentSession.Unique,
			callbackInt(int64(pendingBatch.ID)),
		)))
	}
	for _, operation := range operations {
		rows = append(rows, markup.Row(markup.Data(
			operationButtonText(operation),
			buttonOperation.Unique,
			callbackInt(operation.TransferID),
		)))
	}
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func callbackArguments(arguments []string) []string {
	if len(arguments) == 1 && arguments[0] == "" {
		return nil
	}
	return arguments
}

func (handler *Handler) openOperation(ctx tele.Context) error {
	transferID, err := parsePositiveInt64(ctx.Args(), 0, 1)
	if err != nil {
		return staleCallback(ctx)
	}
	return handler.renderOperation(ctx, transferID, "")
}

func (handler *Handler) ShowOperation(
	ctx tele.Context,
	transferID int64,
	notice string,
) error {
	return handler.renderOperation(ctx, transferID, notice)
}

func (handler *Handler) renderOperation(
	ctx tele.Context,
	transferID int64,
	notice string,
) error {
	details, err := handler.statistics.GetOperation(handler.ctx, transferID)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinets, err := handler.statistics.ListCabinetProgress(handler.ctx, transferID)
	if err != nil {
		return presentError(ctx, err)
	}
	attention, err := handler.statistics.ListAttention(handler.ctx, statistics_service.AttentionFilter{
		TransferID: transferID,
		Limit:      1,
	})
	if err != nil {
		return presentError(ctx, err)
	}

	operation := details.Operation
	status := operationStatus(operation)
	text := fmt.Sprintf(
		"📊 <b>Статистика сеанса №%d</b>\n\n"+
			"ℹ️ Статус: <b>%s</b>\n\n"+
			"%s\n\n"+
			"🏬 <b>Кабинеты</b>",
		operation.SessionID,
		status,
		formatTaskSummary(summarizeCabinetProgress(cabinets)),
	)
	if len(cabinets) == 0 {
		text += "\n— кабинеты пока не назначены"
	}
	if notice != "" {
		text += "\n\n" + notice
	}

	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, len(cabinets)+5)
	for _, cabinet := range cabinets {
		rows = append(rows, markup.Row(markup.Data(
			handler.cabinetButtonText(cabinet),
			buttonCabinetTasks.Unique,
			callbackInt(transferID),
			callbackInt(cabinet.TargetID),
			callbackInt(1),
		)))
	}
	if operation.Phase != "finished" {
		rows = append(rows, markup.Row(markup.Data(
			buttonCurrentSession.Text,
			buttonCurrentSession.Unique,
			callbackInt(operation.BatchID),
		)))
	}
	if len(attention) > 0 {
		rows = append(rows, markup.Row(markup.Data(
			buttonAttention.Text,
			buttonAttention.Unique,
			callbackInt(transferID),
		)))
	}
	rows = append(rows, markup.Row(buttonStatistics))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func (handler *Handler) openAction(ctx tele.Context) error {
	_, actionID, err := parseTwoIDs(ctx.Args())
	if err != nil {
		return staleCallback(ctx)
	}
	details, err := handler.statistics.GetAction(handler.ctx, actionID)
	if err != nil {
		return presentError(ctx, err)
	}
	return handler.renderOperation(ctx, details.Action.TransferID, "")
}

func operationButtonText(row statistics_service.OperationRow) string {
	icon := "⏳"
	if row.Phase == "finished" {
		icon = "✅"
	}
	if row.AttentionCode != "" || row.Outcome == "unresolved" || row.Outcome == "failed" {
		icon = "⚠️"
	}
	return fmt.Sprintf("%s Сеанс №%d · %s", icon, row.SessionID, operationStatus(row))
}

func operationStatus(row statistics_service.OperationRow) string {
	if row.Phase == "finished" {
		switch row.Outcome {
		case "succeeded":
			return "Завершено"
		case "partial":
			return "Завершено частично"
		case "rejected":
			return "Карточки не отправлены"
		case "cancelled":
			return "Отменено"
		default:
			return "Нужна проверка"
		}
	}
	switch row.Phase {
	case "initializing":
		return "Принимаем данные"
	case "preparing":
		return "Проверяем карточки"
	case "awaiting_authorization":
		return "Запускаем отправку"
	case "publishing":
		return "Отправляем в WB"
	case "reconciling":
		return "Проверяем результат"
	case "media":
		return "Завершаем перенос"
	default:
		return "В работе"
	}
}

func parsePositiveInt64(arguments []string, index, expected int) (int64, error) {
	return core_transport_telegram.ParseInt64Argument(
		arguments,
		index,
		expected,
		36,
		1,
	)
}

func callbackInt(value int64) string {
	return strconv.FormatInt(value, 36)
}

func parseTwoIDs(arguments []string) (int64, int64, error) {
	first, err := parsePositiveInt64(arguments, 0, 2)
	if err != nil {
		return 0, 0, err
	}
	second, err := parsePositiveInt64(arguments, 1, 2)
	return first, second, err
}

func staleCallback(ctx tele.Context) error {
	return core_transport_telegram.Notify(ctx, "statistics.stale_callback", "Кнопка устарела. Откройте статистику заново.")
}
