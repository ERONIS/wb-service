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
			core_transport_telegram.FormatProgressBar(summary.Terminal, summary.Total, 10),
			core_transport_telegram.CalcPercent(summary.Terminal, summary.Total),
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
		core_transport_telegram.TruncateRunes(
			strings.TrimSpace(handler.cabinetName(cabinet.CabinetID)),
			40,
		),
		core_transport_telegram.CalcPercent(cabinet.Terminal, cabinet.Total),
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
			"\n\n%d. %s · %s",
			int64(index)+start,
			escapeLimited(task.VendorCode, 90),
			cabinetTaskStatus(task, operation.Phase),
		)
	}
	end := start + int64(len(tasks.Tasks)) - 1
	text += fmt.Sprintf("\n\nПоказано: %d–%d из %d", start, end, tasks.Total)

	markup := handler.bot.NewMarkup()
	rows := make([]tele.Row, 0, 5)
	if navRow, ok := core_transport_telegram.BuildPaginationRow(markup, core_transport_telegram.PaginationConfig{
		Page:           int(page),
		TotalPages:     int(totalPages),
		CallbackUnique: buttonCabinetTasks.Unique,
		ArgPrefix: []string{
			callbackInt(transferID),
			callbackInt(targetID),
		},
		PageEncoder: func(p int) string {
			return callbackInt(int64(p))
		},
	}); ok {
		rows = append(rows, navRow)
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

var terminalOutcomeLabels = map[string]string{
	"success":        "✅ Готово",
	"skipped":        "⏭️ Пропущено",
	"rejected":       "❌ Не отправлено",
	"partial":        "⚠️ Завершено частично",
	"unresolved":     "⚠️ Нужна проверка",
	"internal_error": "❌ Ошибка обработки",
}

var phaseLabels = map[string]string{
	"preparing":              "🔎 Проверяется",
	"awaiting_authorization": "🕐 Готовится к отправке",
	"publishing":             "🛠️ Создаётся карточка",
	"reconciling":            "🕐 Проверяется в WB",
	"media":                  "📸 Загружается фото",
}

func cabinetTaskPages(total int64) int64 {
	return core_transport_telegram.CalculateTotalPages(total, int64(statistics_service.CabinetPageSize))
}

func cabinetTaskStatus(task statistics_service.CabinetTaskRow, phase string) string {
	if task.MediaActionState == "terminal" && (task.OutcomeClass == "success" || task.OutcomeClass == "skipped") {
		if task.OverallOutcome == "unresolved" || task.OverallOutcome == "internal_error" || task.OverallOutcome == "partial" {
			return mediaFailureStatus(task.MediaOutcomeCode)
		}
	}
	if task.OverallOutcome != "" && task.OverallOutcome != "running" {
		if label, ok := terminalOutcomeLabels[task.OverallOutcome]; ok {
			return label
		}
		return terminalItemStatus(task)
	}
	if task.State == "terminal" {
		return terminalItemStatus(task)
	}
	switch task.MediaStatus {
	case "running":
		if task.MediaActionState == "reconciling" {
			return "🕐 Проверяются фото в WB"
		}
		return "📸 Загружается фото"
	case "not_started":
		if task.MediaActionState == "planned" || task.PublicationStatus == "succeeded" || task.PublicationStatus == "skipped" {
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
	if task.PreparationStatus == "running" {
		return "🔎 Проверяется"
	}
	if task.PreparationStatus == "not_started" || task.State == "pending" {
		return "🟡 В очереди"
	}
	if task.State == "running" {
		if label, ok := phaseLabels[phase]; ok {
			return label
		}
		return "🔵 В работе"
	}
	return "❔ Статус неизвестен"
}

func mediaFailureStatus(code string) string {
	switch {
	case code == "WB_MEDIA_TRANSPORT_RATE_LIMITED":
		return "⚠️ Фото: превышен лимит WB"
	case strings.HasPrefix(code, "WB_MEDIA_PARTIAL_UPLOAD"):
		return "⚠️ Фото загружены частично"
	case code == "WB_MEDIA_VISIBILITY_TIMEOUT":
		return "⚠️ Не подтверждена загрузка всех фото"
	case code == "WB_MEDIA_ENVELOPE_REJECTED":
		return "⚠️ WB отклонил загрузку фото"
	default:
		return "⚠️ Требуется проверка фото"
	}
}

func terminalItemStatus(task statistics_service.CabinetTaskRow) string {
	if task.OutcomeClass == "success" && task.NMID > 0 {
		return fmt.Sprintf("✅ Готово · nmID %d", task.NMID)
	}
	if label, ok := terminalOutcomeLabels[task.OutcomeClass]; ok {
		return label
	}
	return "❔ Статус неизвестен"
}

func escapeLimited(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	return html.EscapeString(core_transport_telegram.TruncateRunes(value, limit))
}
