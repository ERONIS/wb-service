package cardimport_telegram_transport

import (
	core_tg_identity "github.com/ERONIS/wb-service/internal/core/transport/telegram/identity"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

func (h *Handler) showCardsMenu(ctx tele.Context) error {
	markup := h.bot.NewMarkup()
	markup.Inline(
		markup.Row(buttonTransfer),
		markup.Row(core_tg_menu.MainMenuButton()),
	)

	return ctx.EditOrSend(
		"📦 <b>Карточки</b>\n\nВыберите действие:",
		markup,
	)
}

func (h *Handler) startTransfer(ctx tele.Context) error {
	authorTelegramID, err := core_tg_identity.SenderID(ctx)
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
	authorTelegramID, err := core_tg_identity.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	sessionID, err := parseSessionID(ctx.Args())
	if err != nil {
		return core_tg_menu.Notify(ctx, "cardimport.session_argument", "⚠️ Не удалось определить текущую загрузку.")
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
		markup.Row(core_tg_menu.MainMenuButton()),
	)

	return ctx.EditOrSend(
		"Загрузка карточек отменена.",
		markup,
	)
}

func (h *Handler) continueImport(ctx tele.Context) error {
	authorTelegramID, err := core_tg_identity.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	command, err := parseContinueCommand(ctx.Args())
	if err != nil {
		return core_tg_menu.Notify(ctx, "cardimport.stale_continue", "⚠️ Кнопка устарела. Откройте текущую загрузку заново.")
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
	user, err := core_tg_identity.UserFromContext(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	command, err := parseFinalizeCommand(ctx.Args())
	if err != nil {
		return core_tg_menu.Notify(ctx, "cardimport.stale_finalize", "⚠️ Кнопка устарела. Откройте новый перенос заново.")
	}

	batch, err := h.service.Finalize(h.ctx, user, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return h.completion.ShowFinalizedSession(ctx, batch)
}
