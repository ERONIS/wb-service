package cardimport_telegram_transport

import (
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

func (h *Handler) showCardsMenu(ctx tele.Context) error {
	markup := h.bot.NewMarkup()
	actions := make([]tele.Btn, 0, 1+len(h.additionalMenuButtons))
	actions = append(actions, buttonTransfer)
	actions = append(actions, h.additionalMenuButtons...)
	rows := make([]tele.Row, 0, len(actions)+1)
	for _, action := range actions {
		rows = append(rows, markup.Row(action))
	}
	rows = append(rows, markup.Row(core_transport_telegram.MainMenuButton()))
	markup.Inline(rows...)

	return ctx.EditOrSend(
		"📦 <b>Карточки</b>\n\nВыберите действие:",
		markup,
	)
}

func (h *Handler) startTransfer(ctx tele.Context) error {
	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
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
	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	sessionID, err := parseSessionID(ctx.Args())
	if err != nil {
		return core_transport_telegram.Notify(ctx, "cardimport.session_argument", "⚠️ Не удалось определить текущую загрузку.")
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
		"Загрузка карточек отменена.",
		markup,
	)
}

func (h *Handler) continueImport(ctx tele.Context) error {
	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	command, err := parseContinueCommand(ctx.Args())
	if err != nil {
		return core_transport_telegram.Notify(ctx, "cardimport.stale_continue", "⚠️ Кнопка устарела. Откройте текущую загрузку заново.")
	}
	command.AuthorTelegramID = authorTelegramID

	view, err := h.service.Continue(h.ctx, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	text, markup := h.sessionView(view)
	return ctx.EditOrSend(
		"➡️ Ошибочные строки и карточки пропущены.\n\n"+text,
		markup,
	)
}

func (h *Handler) finalize(ctx tele.Context) error {
	actor, err := core_transport_telegram.ActorFromContext(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	command, err := parseFinalizeCommand(ctx.Args())
	if err != nil {
		return core_transport_telegram.Notify(ctx, "cardimport.stale_finalize", "⚠️ Кнопка устарела. Откройте новый перенос заново.")
	}

	batch, err := h.service.Finalize(h.ctx, actor, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	h.processing.NotifyFinalized()
	return h.completion.ShowFinalizedSession(ctx, batch)
}
