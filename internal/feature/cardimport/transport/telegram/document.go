package cardimport_telegram_transport

import (
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	tele "gopkg.in/telebot.v3"
)

func (h *Handler) receiveDocument(ctx tele.Context) error {
	authorTelegramID, err := senderTelegramID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	message := ctx.Message()
	if message == nil || message.Document == nil {
		return ctx.Send("⚠️ Не удалось прочитать документ.")
	}
	document := message.Document
	if !cardimport_xlsx_transport.IsFile(document.FileName, document.MIME) {
		return ctx.Send("⚠️ Нужен файл в формате .xlsx.")
	}

	view, err := h.service.GetActiveView(h.ctx, authorTelegramID)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	file, err := h.service.ReserveFile(
		h.ctx,
		cardimport_service.ReserveFileCommand{
			AuthorTelegramID:     authorTelegramID,
			SessionID:            view.ID,
			TelegramFileID:       document.FileID,
			TelegramFileUniqueID: document.UniqueID,
			TelegramMessageID:    int64(message.ID),
			OriginalFilename:     document.FileName,
			MIMEType:             document.MIME,
			DeclaredSize:         document.FileSize,
		},
	)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	command := cardimport_service.ParseFileCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        view.ID,
		FileID:           file.ID,
	}

	switch file.Status {
	case cardimport_service.FileStatusReserved:
		reader, openErr := h.bot.File(&document.File)
		if openErr != nil {
			return ctx.Send("❌ Не удалось скачать файл из Telegram. Отправьте его ещё раз.")
		}

		_, storeErr := h.service.StoreFile(
			h.ctx,
			cardimport_service.StoreFileCommand(command),
			reader,
		)
		_ = reader.Close()
		if storeErr != nil {
			return sendServiceError(ctx, storeErr)
		}

	case cardimport_service.FileStatusStored:
		// Durable blob уже сохранён: продолжаем оборванную обработку.

	case cardimport_service.FileStatusParsing:
		return ctx.Send("⏳ Этот файл уже обрабатывается.")

	case cardimport_service.FileStatusValid,
		cardimport_service.FileStatusInvalid:
		return h.sendSessionView(ctx, view)

	default:
		return ctx.Send("⚠️ Этот файл больше нельзя обработать.")
	}

	parsedView, err := h.service.ParseFile(h.ctx, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return h.sendSessionView(ctx, parsedView)
}
