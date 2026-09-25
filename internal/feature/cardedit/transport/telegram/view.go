package cardedit_telegram_transport

import (
	"fmt"
	"html"
	"strings"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

func (handler *Handler) confirmMarkup() *tele.ReplyMarkup {
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonConfirm, buttonCancel),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return markup
}

func (handler *Handler) cancelMarkup() *tele.ReplyMarkup {
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonCancel),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return markup
}

func (handler *Handler) retryMarkup() *tele.ReplyMarkup {
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonContinue),
		markup.Row(buttonCancel),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return markup
}

func (handler *Handler) backMarkup() *tele.ReplyMarkup {
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonEdit),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	return markup
}

func foundText(vendorCode string, targets []cardedit_service.Target) string {
	var builder strings.Builder
	fmt.Fprintf(
		&builder,
		"🔎 Артикул: %s\n\nНайдено карточек: %d",
		html.EscapeString(vendorCode),
		len(targets),
	)
	for _, target := range targets {
		fmt.Fprintf(
			&builder,
			"\n• %s — nmID %d",
			html.EscapeString(target.Cabinet.Name),
			target.NMID,
		)
	}
	builder.WriteString("\n\nПодтвердить редактирование этих карточек?")
	return builder.String()
}

func parseIssuesText(issues []cardimport_service.ParseIssue) string {
	var builder strings.Builder
	builder.WriteString("❌ <b>В Excel-файле есть ошибки:</b>")
	shown := 0
	for _, issue := range issues {
		if issue.Severity != cardimport_service.IssueSeverityError {
			continue
		}
		if shown >= 6 {
			builder.WriteString("\n• …показаны первые 6 ошибок")
			break
		}
		location := ""
		if issue.Row > 0 {
			location = fmt.Sprintf("строка %d: ", issue.Row)
		}
		fmt.Fprintf(
			&builder,
			"\n• %s%s",
			location,
			html.EscapeString(core_transport_telegram.TruncateRunes(issue.Message, 220)),
		)
		shown++
	}
	builder.WriteString("\n\nСессия сохранена. Исправьте файл и нажмите «Продолжить».")
	return builder.String()
}

func (handler *Handler) NotifyEditResult(
	telegramID int64,
	result cardedit_service.Result,
) error {
	var builder strings.Builder
	fmt.Fprintf(
		&builder,
		"✏️ <b>Редактирование</b> %s:\n",
		html.EscapeString(result.VendorCode),
	)
	for _, target := range result.Targets {
		status := "✅ Подтверждено"
		switch {
		case target.Err != nil:
			status = "❌ " + html.EscapeString(core_transport_telegram.TruncateRunes(target.Err.Error(), 260))
		case len(target.WBErrors) > 0:
			status = "❌ WB: " + html.EscapeString(core_transport_telegram.TruncateRunes(strings.Join(target.WBErrors, "; "), 260))
		case target.Unconfirmed:
			status = "⏳ Отправлено, но WB ещё не подтвердил"
		case !target.Confirmed:
			status = "⚠️ Результат не определён"
		}
		fmt.Fprintf(
			&builder,
			"\n• %s (nmID %d) — %s",
			html.EscapeString(target.CabinetName),
			target.NMID,
			status,
		)
	}
	markup := handler.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonEdit),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)
	_, err := handler.bot.Send(
		&tele.User{ID: telegramID},
		builder.String(),
		tele.ModeHTML,
		markup,
	)
	return err
}

var _ cardedit_service.Notifier = (*Handler)(nil)
