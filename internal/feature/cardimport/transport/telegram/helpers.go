package cardimport_telegram_transport

import (
	"errors"
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

func senderTelegramID(ctx tele.Context) (int64, error) {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return 0, fmt.Errorf(
			"Telegram sender is unavailable: %w",
			core_errors.ErrInvalidArgument,
		)
	}

	return sender.ID, nil
}

func senderTrustedActor(
	ctx tele.Context,
) (cardimport_service.TrustedActor, error) {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return cardimport_service.TrustedActor{}, fmt.Errorf(
			"Telegram sender is unavailable: %w",
			core_errors.ErrInvalidArgument,
		)
	}

	displayName := strings.TrimSpace(strings.Join(
		[]string{sender.FirstName, sender.LastName},
		" ",
	))
	if displayName == "" {
		displayName = strings.TrimSpace(sender.Username)
	}
	if displayName == "" {
		displayName = fmt.Sprintf("Telegram %d", sender.ID)
	}
	displayName = truncateText(displayName, 100)

	return cardimport_service.TrustedActor{
		TelegramUserID: sender.ID,
		DisplayName:    displayName,
	}, nil
}

func parseSessionID(arguments []string) (cardimport_service.SessionID, error) {
	if len(arguments) != 1 {
		return 0, fmt.Errorf("unexpected arguments count: %d", len(arguments))
	}

	value, err := strconv.ParseInt(arguments[0], 10, 64)
	if err != nil || value <= 0 {
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

	sessionValue, err := strconv.ParseInt(arguments[0], 10, 64)
	if err != nil || sessionValue <= 0 {
		return cardimport_service.FinalizeCommand{}, fmt.Errorf(
			"invalid session ID %q",
			arguments[0],
		)
	}
	revision, err := strconv.ParseInt(arguments[1], 10, 64)
	if err != nil || revision < 0 {
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

func (h *Handler) sendSessionView(
	ctx tele.Context,
	view cardimport_service.SessionView,
) error {
	text, markup := h.sessionView(view)
	return ctx.EditOrSend(text, markup)
}

const uploadedScreenDebounce = 350 * time.Millisecond

// sendUploadedSessionView refreshes the existing active menu. A short debounce
// combines Telegram multi-file sends into one Telegram edit.
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

	authorTelegramID, err := senderTelegramID(ctx)
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
	rows := make([]tele.Row, 0, 3)
	if view.Ready {
		rows = append(rows, markup.Row(markup.Data(
			buttonFinalize.Text,
			buttonFinalize.Unique,
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
	fmt.Fprintf(
		&builder,
		"📦 <b>Загрузка карточек</b>\n\nФайлы: %d/%d\nКарточки: %d\nОшибки: %d\n",
		len(view.Files),
		cardimport_service.MaxFilesPerSession,
		view.CardsCount,
		view.ErrorsCount,
	)

	for _, file := range view.Files {
		fmt.Fprintf(
			&builder,
			"\n%s <code>%s</code> — карточек: %d, ошибок: %d",
			fileStatusIcon(file.Status),
			html.EscapeString(truncateText(file.OriginalFilename, 64)),
			file.CardsCount,
			file.ErrorsCount,
		)
	}

	if len(view.Issues) > 0 {
		builder.WriteString("\n\n<b>Первые ошибки:</b>")
		for index, issue := range view.Issues {
			if index >= 8 {
				builder.WriteString("\n• …показаны первые 8 ошибок")
				break
			}

			location := ""
			if issue.Filename != "" {
				location = html.EscapeString(
					truncateText(issue.Filename, 64),
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
				html.EscapeString(truncateText(issue.Issue.Message, 220)),
			)
		}
	}

	if view.Ready {
		builder.WriteString("\n\n✅ Все файлы разобраны без блокирующих ошибок.")
	} else {
		builder.WriteString("\n\nОтправьте следующий XLSX-файл документом.")
	}

	return builder.String()
}

func truncateText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}

	return string(runes[:maxRunes-1]) + "…"
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
	key, text := serviceErrorNotification(err)
	return core_transport_telegram.Notify(ctx, key, text)
}

func serviceErrorNotification(err error) (string, string) {
	key := "cardimport.internal"
	text := "❌ Не удалось выполнить операцию. Попробуйте позже."
	switch {
	case errors.Is(err, core_errors.ErrNotFound):
		key = "cardimport.session_not_found"
		text = "⚠️ Сначала откройте «Карточки» → «Перенос карточек»."
	case errors.Is(err, core_errors.ErrConflict):
		key = "cardimport.session_conflict"
		text = "⚠️ Файл или текущая загрузка уже изменились. Обновите экран."
	case errors.Is(err, core_errors.ErrInvalidArgument):
		key = "cardimport.invalid_file"
		text = "⚠️ Файл не подходит: проверьте формат и размер."
	case errors.Is(err, core_errors.ErrForbidden):
		key = "cardimport.forbidden"
		text = "⛔ Недостаточно прав."
	}

	return key, text
}
