package cardimport_telegram_transport

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

const sessionViewFilesLimit = 10

func parseSessionID(arguments []string) (cardimport_service.SessionID, error) {
	if len(arguments) != 1 {
		return 0, fmt.Errorf("unexpected arguments count: %d", len(arguments))
	}

	value, err := core_transport_telegram.ParseInt64Argument(
		arguments, 0, 1, 10, 1,
	)
	if err != nil {
		return 0, fmt.Errorf("invalid session ID %q", arguments[0])
	}

	return cardimport_service.SessionID(value), nil
}

func parseFinalizeCommand(
	arguments []string,
) (cardimport_service.FinalizeCommand, error) {
	if len(arguments) != 2 {
		return cardimport_service.FinalizeCommand{}, fmt.Errorf(
			"unexpected arguments count: %d",
			len(arguments),
		)
	}

	sessionValue, err := core_transport_telegram.ParseInt64Argument(
		arguments, 0, 2, 10, 1,
	)
	if err != nil {
		return cardimport_service.FinalizeCommand{}, fmt.Errorf(
			"invalid session ID %q",
			arguments[0],
		)
	}
	revision, err := core_transport_telegram.ParseInt64Argument(
		arguments, 1, 2, 10, 0,
	)
	if err != nil {
		return cardimport_service.FinalizeCommand{}, fmt.Errorf(
			"invalid session revision %q",
			arguments[1],
		)
	}

	return cardimport_service.FinalizeCommand{
		SessionID:        cardimport_service.SessionID(sessionValue),
		ExpectedRevision: revision,
		IdempotencyKey: fmt.Sprintf(
			"telegram:%d:%d",
			sessionValue,
			revision,
		),
	}, nil
}

func parseContinueCommand(
	arguments []string,
) (cardimport_service.ContinueCommand, error) {
	if len(arguments) != 2 {
		return cardimport_service.ContinueCommand{}, fmt.Errorf(
			"unexpected arguments count: %d",
			len(arguments),
		)
	}

	sessionValue, err := core_transport_telegram.ParseInt64Argument(
		arguments, 0, 2, 10, 1,
	)
	if err != nil {
		return cardimport_service.ContinueCommand{}, fmt.Errorf(
			"invalid session ID %q",
			arguments[0],
		)
	}
	revision, err := core_transport_telegram.ParseInt64Argument(
		arguments, 1, 2, 10, 0,
	)
	if err != nil {
		return cardimport_service.ContinueCommand{}, fmt.Errorf(
			"invalid session revision %q",
			arguments[1],
		)
	}

	return cardimport_service.ContinueCommand{
		SessionID:        cardimport_service.SessionID(sessionValue),
		ExpectedRevision: revision,
	}, nil
}

func (h *Handler) sendSessionView(
	ctx tele.Context,
	view cardimport_service.SessionView,
) error {
	text, markup := h.sessionView(view)
	return ctx.EditOrSend(text, markup)
}

const uploadedScreenDebounce = 350 * time.Millisecond

// sendUploadedSessionView places the current controls below uploaded files. A
// short debounce combines Telegram multi-file sends into one new menu message.
func (h *Handler) sendUploadedSessionView(
	ctx tele.Context,
	view cardimport_service.SessionView,
) error {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		text, markup := h.sessionView(view)
		return ctx.EditOrSend(text, markup)
	}

	h.uploadScreenMu.Lock()
	h.uploadScreenGeneration[chat.ID]++
	generation := h.uploadScreenGeneration[chat.ID]
	h.uploadScreenMu.Unlock()

	timer := time.NewTimer(uploadedScreenDebounce)
	defer timer.Stop()
	select {
	case <-h.ctx.Done():
		return nil
	case <-timer.C:
	}

	h.uploadScreenMu.Lock()
	if h.uploadScreenGeneration[chat.ID] != generation {
		h.uploadScreenMu.Unlock()
		return nil
	}
	delete(h.uploadScreenGeneration, chat.ID)
	h.uploadScreenMu.Unlock()

	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	latestView, err := h.service.GetView(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        view.ID,
	})
	if err != nil {
		return sendServiceError(ctx, err)
	}

	text, markup := h.sessionView(latestView)
	return ctx.EditOrSend(text, markup)
}

func (h *Handler) sessionView(
	view cardimport_service.SessionView,
) (string, *tele.ReplyMarkup) {
	markup := h.bot.NewMarkup()
	rows := make([]tele.Row, 0, 5)
	unfinishedCount := unfinishedFilesCount(view)
	if view.Ready {
		rows = append(rows, markup.Row(markup.Data(
			buttonFinalize.Text,
			buttonFinalize.Unique,
			strconv.FormatInt(int64(view.ID), 10),
			strconv.FormatInt(view.Revision, 10),
		)))
	}
	if view.ErrorsCount > 0 && unfinishedCount == 0 {
		rows = append(rows, markup.Row(markup.Data(
			buttonContinue.Text,
			buttonContinue.Unique,
			strconv.FormatInt(int64(view.ID), 10),
			strconv.FormatInt(view.Revision, 10),
		)))
	}
	rows = append(
		rows,
		markup.Row(markup.Data(
			buttonCancel.Text,
			buttonCancel.Unique,
			strconv.FormatInt(int64(view.ID), 10),
		)),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	markup.Inline(rows...)
	return sessionViewText(view), markup
}

func sessionViewText(view cardimport_service.SessionView) string {
	var builder strings.Builder
	fileCountText := fmt.Sprintf("Файлы: %d", len(view.Files))
	if cardimport_service.MaxFilesPerSession > 0 {
		fileCountText = fmt.Sprintf("Файлы: %d/%d", len(view.Files), cardimport_service.MaxFilesPerSession)
	}
	fmt.Fprintf(
		&builder,
		"📦 <b>Загрузка карточек</b>\n\n%s\nКарточки: %d\nОшибки: %d\n",
		fileCountText,
		view.CardsCount,
		view.ErrorsCount,
	)

	firstVisibleFile := len(view.Files) - sessionViewFilesLimit
	if firstVisibleFile < 0 {
		firstVisibleFile = 0
	}
	if firstVisibleFile > 0 {
		fmt.Fprintf(
			&builder,
			"\n…предыдущих файлов: %d",
			firstVisibleFile,
		)
	}
	for _, file := range view.Files[firstVisibleFile:] {
		fmt.Fprintf(
			&builder,
			"\n%s %s — карточек: %d, ошибок: %d",
			fileStatusIcon(file.Status),
			html.EscapeString(core_transport_telegram.TruncateRunes(file.OriginalFilename, 64)),
			file.CardsCount,
			file.ErrorsCount,
		)
	}

	visibleIssues := visibleFirstIssues(view.Issues)
	if len(visibleIssues) > 0 {
		builder.WriteString("\n\n<b>Первые ошибки:</b>")
		for index, issue := range visibleIssues {
			if index >= 8 {
				builder.WriteString("\n• …показаны первые 8 ошибок")
				break
			}

			location := ""
			if issue.Filename != "" {
				location = html.EscapeString(
					core_transport_telegram.TruncateRunes(issue.Filename, 64),
				)
			}
			if issue.Issue.Row > 0 {
				location += fmt.Sprintf(" — строка %d", issue.Issue.Row)
			}
			if location != "" {
				location += ": "
			}
			fmt.Fprintf(
				&builder,
				"\n• %s%s",
				location,
				html.EscapeString(core_transport_telegram.TruncateRunes(issue.Issue.Message, 220)),
			)
		}
	}

	unfinishedCount := unfinishedFilesCount(view)
	if unfinishedCount > 0 {
		fmt.Fprintf(
			&builder,
			"\n\n⏳ Обрабатываются файлы: %d. При сетевом сбое бот продолжит автоматически.",
			unfinishedCount,
		)
	} else if view.Ready {
		builder.WriteString("\n\n✅ Все файлы разобраны без блокирующих ошибок.")
	} else if view.ErrorsCount > 0 {
		builder.WriteString("\n\nОшибочные строки и карточки можно пропустить кнопкой «Продолжить». Остальные корректные карточки сохранятся.")
	} else {
		builder.WriteString("\n\nОтправьте следующий XLSX-файл документом.")
	}

	return builder.String()
}

func visibleFirstIssues(issues []cardimport_service.IssueView) []cardimport_service.IssueView {
	visible := make([]cardimport_service.IssueView, 0, len(issues))
	for _, issue := range issues {
		switch issue.Issue.Code {
		case "vendor_code_across_files",
			"barcode_across_files",
			"duplicate_barcode_in_file":
			continue
		default:
			visible = append(visible, issue)
		}
	}
	return visible
}

func unfinishedFilesCount(view cardimport_service.SessionView) int {
	count := 0
	for _, file := range view.Files {
		switch file.Status {
		case cardimport_service.FileStatusReserved,
			cardimport_service.FileStatusStored,
			cardimport_service.FileStatusParsing:
			count++
		}
	}
	return count
}

func recoverableFilesCount(
	view cardimport_service.SessionView,
	now time.Time,
) int {
	count := 0
	for _, file := range view.Files {
		if isRecoverableFile(file.File, now) {
			count++
		}
	}
	return count
}

func fileStatusIcon(status cardimport_service.FileStatus) string {
	switch status {
	case cardimport_service.FileStatusValid:
		return "✅"
	case cardimport_service.FileStatusInvalid:
		return "❌"
	case cardimport_service.FileStatusParsing:
		return "⏳"
	case cardimport_service.FileStatusStored:
		return "💾"
	default:
		return "📎"
	}
}

func sendServiceError(ctx tele.Context, err error) error {
	return core_transport_telegram.NotifyError(
		ctx,
		err,
		core_transport_telegram.OnError(nil, "cardimport.internal", "❌ Не удалось выполнить операцию. Попробуйте позже."),
		core_transport_telegram.OnError(core_errors.ErrNotFound, "cardimport.session_not_found", "⚠️ Сначала откройте «Карточки» → «Перенос карточек»."),
		core_transport_telegram.OnError(core_errors.ErrConflict, "cardimport.session_conflict", "⚠️ Файл или текущая загрузка уже изменились. Обновите экран."),
		core_transport_telegram.OnError(core_errors.ErrInvalidArgument, "cardimport.invalid_file", "⚠️ Файл не подходит: проверьте формат и размер."),
		core_transport_telegram.OnError(core_errors.ErrForbidden, "cardimport.forbidden", "⛔ Недостаточно прав."),
	)
}
