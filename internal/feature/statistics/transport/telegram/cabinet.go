package statistics_telegram_transport

import (
	"fmt"
	"html"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	tele "gopkg.in/telebot.v3"
)

type taskSummary struct {
	Total    int64
	Pending  int64
	Running  int64
	Terminal int64
	Ready    int64
	Errors   int64
}

func summarizeCabinetProgress(cabinets []statistics_service.CabinetProgressRow) taskSummary {
	var result taskSummary
	for _, cabinet := range cabinets {
		result.Total += cabinet.Total
		result.Pending += cabinet.Pending
		result.Running += cabinet.Running
		result.Terminal += cabinet.Terminal
		result.Ready += cabinet.Ready
		result.Errors += cabinet.Errors
	}
	return result
}

func summaryFromCabinet(cabinet statistics_service.CabinetProgressRow) taskSummary {
	return taskSummary{
		Total:    cabinet.Total,
		Pending:  cabinet.Pending,
		Running:  cabinet.Running,
		Terminal: cabinet.Terminal,
		Ready:    cabinet.Ready,
		Errors:   cabinet.Errors,
	}
}

func formatTaskSummary(summary taskSummary) string {
	var totalLine string
	if summary.Total == 0 {
		totalLine = "Всего: <b>0</b>"
	} else {
		totalLine = fmt.Sprintf(
			"Всего: <b>%d</b> · %s %d%%",
			summary.Total,
			formatProgressBar(summary.Terminal, summary.Total, 10),
			calcPercent(summary.Terminal, summary.Total),
		)
	}
	return fmt.Sprintf(
		"📦 <b>Задачи по кабинетам</b>\n"+
			"%s\n"+
			"🟡 В очереди: %d\n"+
			"🔵 В работе: %d\n"+
			"🔴 Ошибки: %d\n"+
			"🟢 Готово: %d",
		totalLine,
		summary.Pending,
		summary.Running,
		summary.Errors,
		summary.Ready,
	)
}

func calcPercent(done, total int64) int64 {
	if total <= 0 {
		return 0
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	return (done*100 + total/2) / total
}

func formatProgressBar(done, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	filled := int(done * int64(width) / total)
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

func (handler *Handler) cabinetName(cabinetID string) string {
	name := strings.TrimSpace(handler.cabinetNames[cabinetID])
	if name == "" {
		name = strings.TrimSpace(cabinetID)
	}
	if name == "" {
		return "Кабинет"
	}
	return name
}

func (handler *Handler) cabinetButtonText(cabinet statistics_service.CabinetProgressRow) string {
	icon := "🟡"
	switch {
	case cabinet.Attention > 0:
		icon = "⚠️"
	case cabinet.Total > 0 && cabinet.Terminal == cabinet.Total && cabinet.Errors == 0:
		icon = "✅"
	case cabinet.Total > 0 && cabinet.Terminal == cabinet.Total:
		icon = "❌"
	case cabinet.Running > 0 || cabinet.Terminal > 0:
		icon = "🔵"
	}
	return fmt.Sprintf(
		"%s %s · %d%%",
		icon,
		truncateRunes(handler.cabinetName(cabinet.CabinetID), 40),
		calcPercent(cabinet.Terminal, cabinet.Total),
	)
}

func (handler *Handler) openCabinetTasks(ctx tele.Context) error {
	transferID, err := parsePositiveInt64(ctx.Args(), 0, 3)
	if err != nil {
		return staleCallback(ctx)
	}
	targetID, err := parsePositiveInt64(ctx.Args(), 1, 3)
	if err != nil {
		return staleCallback(ctx)
	}
	requestedPage, err := parsePositiveInt64(ctx.Args(), 2, 3)
	if err != nil {
		return staleCallback(ctx)
	}

	details, err := handler.statistics.GetOperation(handler.ctx, transferID)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinets, err := handler.statistics.ListCabinetProgress(handler.ctx, transferID)
	if err != nil {
		return presentError(ctx, err)
	}
	cabinet, found := findCabinetProgress(cabinets, targetID)
	if !found || cabinet.Total <= 0 {
		return staleCallback(ctx)
	}

	totalPages := cabinetTaskPages(cabinet.Total)
	page := requestedPage
	if page > totalPages {
		page = totalPages
	}
	tasks, err := handler.statistics.ListCabinetTasks(
		handler.ctx,
		statistics_service.CabinetTaskFilter{
			TransferID: transferID,
			TargetID:   targetID,
			Limit:      statistics_service.CabinetPageSize,
			Offset:     int((page - 1) * statistics_service.CabinetPageSize),
		},
	)
	if err != nil {
		return presentError(ctx, err)
	}
	if tasks.Total <= 0 || len(tasks.Tasks) == 0 {
		return staleCallback(ctx)
	}

	operation := details.Operation
	name := handler.cabinetName(cabinet.CabinetID)
	text := fmt.Sprintf(
		"🏬 <b>%s</b>\n"+
			"📊 Сеанс №%d\n\n"+
			"%s\n\n"+
			"📋 <b>Все задачи</b> · страница %d/%d",
		escapeLimited(name, 120),
		operation.SessionID,
		formatTaskSummary(summaryFromCabinet(cabinet)),
		page,
		totalPages,
	)
	start := (page-1)*statistics_service.CabinetPageSize + 1
	for index, task := range tasks.Tasks {
		text += fmt.Sprintf(
			"\n\n%d. <code>%s</code> · %s",
			int64(index)+start,
			escapeLimited(task.VendorCode, 90),
			cabinetTaskStatus(task, operation.Phase),
		)
		if groupName := strings.TrimSpace(task.GroupName); groupName != "" {
			text += "\n   📁 " + escapeLimited(groupName, 110)
		}
	}
	end := start + int64(len(tasks.Tasks)) - 1
	text += fmt.Sprintf("\n\nПоказано: %d–%d из %d", start, end, tasks.Total)

	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, 5)
	if totalPages > 1 {
		buttons := make([]tele.Btn, 0, 2)
		if page > 1 {
			buttons = append(buttons, markup.Data(
				"⬅️ Назад",
				buttonCabinetTasks.Unique,
				callbackInt(transferID),
				callbackInt(targetID),
				callbackInt(page-1),
			))
		}
		if page < totalPages {
			buttons = append(buttons, markup.Data(
				"Вперёд ➡️",
				buttonCabinetTasks.Unique,
				callbackInt(transferID),
				callbackInt(targetID),
				callbackInt(page+1),
			))
		}
		if len(buttons) > 0 {
			rows = append(rows, markup.Row(buttons...))
		}
	}
	rows = append(rows, markup.Row(markup.Data(
		fmt.Sprintf("⬅️ Сеанс №%d", operation.SessionID),
		buttonOperation.Unique,
		callbackInt(transferID),
	)))
	rows = append(rows, markup.Row(buttonStatistics))
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)
	return ctx.EditOrSend(text, markup)
}

func findCabinetProgress(
	cabinets []statistics_service.CabinetProgressRow,
	targetID int64,
) (statistics_service.CabinetProgressRow, bool) {
	for _, cabinet := range cabinets {
		if cabinet.TargetID == targetID {
			return cabinet, true
		}
	}
	return statistics_service.CabinetProgressRow{}, false
}

func cabinetTaskPages(total int64) int64 {
	if total <= 0 {
		return 1
	}
	return (total + statistics_service.CabinetPageSize - 1) /
		statistics_service.CabinetPageSize
}

func cabinetTaskStatus(task statistics_service.CabinetTaskRow, phase string) string {
	if task.OverallOutcome != "" && task.OverallOutcome != "running" {
		switch task.OverallOutcome {
		case "rejected":
			return "❌ Не отправлено"
		case "partial":
			return "⚠️ Завершено частично"
		case "unresolved":
			return "⚠️ Нужна проверка"
		case "internal_error":
			return "❌ Ошибка обработки"
		}
		return terminalItemStatus(task)
	}
	if task.State == "terminal" &&
		(task.OutcomeClass == "rejected" || task.OutcomeClass == "partial" ||
			task.OutcomeClass == "unresolved" || task.OutcomeClass == "internal_error") {
		return terminalItemStatus(task)
	}
	switch task.MediaStatus {
	case "running":
		return "📸 Загружается фото"
	case "not_started":
		if task.PublicationStatus == "succeeded" || task.PublicationStatus == "skipped" {
			return "🟡 Фото в очереди"
		}
	}
	switch task.PublicationStatus {
	case "running":
		if phase == "reconciling" {
			return "🕐 Проверяется в WB"
		}
		return "🛠️ Создаётся карточка"
	case "not_started":
		if task.PreparationStatus == "succeeded" {
			return "🟡 Отправка в очереди"
		}
	}
	switch task.PreparationStatus {
	case "running":
		return "🔎 Проверяется"
	case "not_started":
		return "🟡 В очереди"
	}

	switch task.State {
	case "pending":
		return "🟡 В очереди"
	case "running":
		switch phase {
		case "preparing":
			return "🔎 Проверяется"
		case "awaiting_authorization":
			return "🕐 Готовится к отправке"
		case "publishing":
			return "🛠️ Создаётся карточка"
		case "reconciling":
			return "🕐 Проверяется в WB"
		case "media":
			return "📸 Загружается фото"
		default:
			return "🔵 В работе"
		}
	case "terminal":
		return terminalItemStatus(task)
	}
	return "❔ Статус неизвестен"
}

func terminalItemStatus(task statistics_service.CabinetTaskRow) string {
	switch task.OutcomeClass {
	case "success":
		if task.NMID > 0 {
			return fmt.Sprintf("✅ Готово · nmID %d", task.NMID)
		}
		return "✅ Готово"
	case "skipped":
		return "⏭️ Пропущено"
	case "rejected":
		return "❌ Не отправлено"
	case "partial":
		return "⚠️ Завершено частично"
	case "unresolved":
		return "⚠️ Нужна проверка"
	case "internal_error":
		return "❌ Ошибка обработки"
	default:
		return "❔ Статус неизвестен"
	}
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func escapeLimited(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	var result strings.Builder
	for _, symbol := range value {
		escaped := html.EscapeString(string(symbol))
		if result.Len()+len(escaped) > limit {
			result.WriteString("…")
			break
		}
		result.WriteString(escaped)
	}
	return result.String()
}
