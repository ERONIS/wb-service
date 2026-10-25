package cardimport_telegram_transport

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_tg_callback "github.com/ERONIS/wb-service/internal/core/transport/telegram/callback"
	core_tg_identity "github.com/ERONIS/wb-service/internal/core/transport/telegram/identity"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	core_tg_presenter "github.com/ERONIS/wb-service/internal/core/transport/telegram/presenter"
	core_tg_ui "github.com/ERONIS/wb-service/internal/core/transport/telegram/ui"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

	tele "gopkg.in/telebot.v3"
)

const sessionViewFilesLimit = 10

func parseSessionID(arguments []string) (cardimport_model.SessionID, error) {
	value, err := core_tg_callback.ParseInt64Argument(arguments, 0, 1, 10, 1)
	return cardimport_model.SessionID(value), err
}

func parseFinalizeCommand(arguments []string) (cardimport_contract.FinalizeCommand, error) {
	id, revision, err := core_tg_callback.ParseIDAndRevision(arguments)
	if err != nil {
		return cardimport_contract.FinalizeCommand{}, err
	}
	return cardimport_contract.FinalizeCommand{
		SessionID:        cardimport_model.SessionID(id),
		ExpectedRevision: revision,
		IdempotencyKey:   fmt.Sprintf("telegram:%d:%d", id, revision),
	}, nil
}

func (h *Handler) sendSessionView(
	ctx tele.Context,
	view cardimport_model.SessionView,
) error {
	text, markup := h.sessionView(view)
	return ctx.EditOrSend(text, markup)
}

const uploadedScreenDebounce = 350 * time.Millisecond

// sendUploadedSessionView places the current controls below uploaded files. A
// short debounce combines Telegram multi-file sends into one new menu message.
func (h *Handler) sendUploadedSessionView(
	ctx tele.Context,
	view cardimport_model.SessionView,
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

	authorTelegramID, err := core_tg_identity.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	latestView, err := h.service.GetView(h.ctx, cardimport_contract.SessionCommand{
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
	view cardimport_model.SessionView,
) (string, *tele.ReplyMarkup) {
	markup := h.bot.NewMarkup()
	rows := make([]tele.Row, 0, 5)
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
		markup.Row(core_tg_menu.MainMenuButton()),
	)
	markup.Inline(rows...)
	return sessionViewText(view), markup
}

func sessionViewText(view cardimport_model.SessionView) string {
	var builder strings.Builder
	fileCountText := fmt.Sprintf("Файлы: %d", len(view.Files))
	if cardimport_model.MaxFilesPerSession > 0 {
		fileCountText = fmt.Sprintf("Файлы: %d/%d", len(view.Files), cardimport_model.MaxFilesPerSession)
	}
	fmt.Fprintf(
		&builder,
		"📦 <b>Загрузка карточек</b>\n\n%s\nКарточки: %d\nК сохранению: %d\nОшибки: %d\n",
		fileCountText,
		view.CardsCount,
		view.ReadyCardsCount,
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
			html.EscapeString(core_tg_ui.TruncateRunes(file.OriginalFilename, 64)),
			file.CardsCount,
			file.ErrorsCount,
		)
	}

	unfinishedCount := unfinishedFilesCount(view)
	if unfinishedCount > 0 {
		fmt.Fprintf(
			&builder,
			"\n\n⏳ Обрабатываются файлы: %d. При сетевом сбое бот продолжит автоматически.",
			unfinishedCount,
		)
	} else if view.Ready && view.ErrorsCount > 0 {
		builder.WriteString("\n\n✅ По кнопке «Готово» сохранятся только корректные карточки. Ошибочные будут пропущены.")
	} else if view.Ready {
		builder.WriteString("\n\n✅ Все файлы разобраны без блокирующих ошибок.")
	} else if view.ErrorsCount > 0 {
		builder.WriteString("\n\nНет корректных карточек для сохранения. Загрузите исправленный XLSX-файл.")
	} else {
		builder.WriteString("\n\nОтправьте следующий XLSX-файл документом.")
	}
	if len(view.Issues) > 0 {
		builder.WriteString("\n\n<b>Ошибки и замечания:</b>")
		for index, issue := range view.Issues {
			if index >= 8 {
				builder.WriteString("\n• …показаны первые 8 замечаний")
				break
			}
			location := ""
			if issue.Filename != "" {
				location = html.EscapeString(core_tg_ui.TruncateRunes(issue.Filename, 64))
			}
			if issue.Issue.Row > 0 {
				location += fmt.Sprintf(" — строка %d", issue.Issue.Row)
			}
			if location != "" {
				location += ": "
			}
			fmt.Fprintf(&builder, "\n• %s%s", location, html.EscapeString(core_tg_ui.TruncateRunes(issue.Issue.Message, 220)))
		}
	}
	return builder.String()
}

func unfinishedFilesCount(view cardimport_model.SessionView) int {
	count := 0
	for _, file := range view.Files {
		switch file.Status {
		case cardimport_model.FileStatusReserved,
			cardimport_model.FileStatusStored,
			cardimport_model.FileStatusParsing:
			count++
		}
	}
	return count
}

func fileStatusIcon(status cardimport_model.FileStatus) string {
	switch status {
	case cardimport_model.FileStatusValid:
		return "✅"
	case cardimport_model.FileStatusInvalid:
		return "❌"
	case cardimport_model.FileStatusParsing:
		return "⏳"
	case cardimport_model.FileStatusStored:
		return "💾"
	default:
		return "📎"
	}
}

func sendServiceError(ctx tele.Context, err error) error {
	return core_tg_presenter.NotifyServiceError(
		ctx,
		err,
		core_tg_presenter.OnError(core_errors.ErrNotFound, "cardimport.session_not_found", "⚠️ Сначала откройте «Карточки» → «Перенос карточек»."),
		core_tg_presenter.OnError(core_errors.ErrConflict, "cardimport.session_conflict", "⚠️ Файл или текущая загрузка уже изменились. Обновите экран."),
		core_tg_presenter.OnError(core_errors.ErrInvalidArgument, "cardimport.invalid_file", "⚠️ Файл не подходит: проверьте формат и размер."),
	)
}
