package cardimport_telegram_transport

import (
	"fmt"

	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

func (h *Handler) showCardsMenu(ctx tele.Context) error {
	markup := h.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonTransfer),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)

	return ctx.EditOrSend(
		"📦 <b>Карточки</b>\n\nВыберите действие:",
		markup,
	)
}

func (h *Handler) startTransfer(ctx tele.Context) error {
	authorTelegramID, err := senderTelegramID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	session, err := h.service.Begin(h.ctx, cardimport_service.BeginCommand{
		AuthorTelegramID: authorTelegramID,
		Purpose:          cardimport_service.PurposeTransfer,
	})
	if err != nil {
		return sendServiceError(ctx, err)
	}

	view, err := h.service.GetView(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        session.ID,
	})
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return h.sendSessionView(ctx, view)
}

func (h *Handler) cancel(ctx tele.Context) error {
	authorTelegramID, err := senderTelegramID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	sessionID, err := parseSessionID(ctx.Args())
	if err != nil {
		return ctx.EditOrSend("⚠️ Не удалось определить сессию импорта.")
	}

	if err := h.service.Cancel(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        sessionID,
	}); err != nil {
		return sendServiceError(ctx, err)
	}

	markup := h.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonTransfer),
		markup.Row(core_transport_telegram.MainMenuButton()),
	)

	return ctx.EditOrSend(
		fmt.Sprintf("Импорт №%d отменён.", sessionID),
		markup,
	)
}

func (h *Handler) finalize(ctx tele.Context) error {
	actor, err := senderTrustedActor(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	command, err := parseFinalizeCommand(ctx.Args())
	if err != nil {
		return ctx.EditOrSend("⚠️ Кнопка устарела. Откройте импорт заново.")
	}

	batch, err := h.service.Finalize(h.ctx, actor, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	markup := h.bot.NewMarkup()
	markup.Inline(markup.Row(core_transport_telegram.MainMenuButton()))
	return ctx.EditOrSend(
		fmt.Sprintf(
			"✅ Импорт зафиксирован.\n\nBatch №%d\nКарточки: %d\nГруппы: %d\nChecksum: <code>%s</code>\n\nПодготовка переноса будет запущена отдельным обработчиком.",
			batch.ID,
			batch.ItemsCount,
			batch.GroupsCount,
			batch.Checksum.String()[:12],
		),
		markup,
	)
}
