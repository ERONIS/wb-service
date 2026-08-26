package cardimport_telegram_transport

import (
	"errors"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
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
		return core_transport_telegram.Notify(ctx, "cardimport.document_unavailable", "⚠️ Не удалось прочитать документ.")
	}
	document := message.Document
	if !cardimport_xlsx_transport.IsFile(document.FileName, document.MIME) {
		return core_transport_telegram.Notify(ctx, "cardimport.invalid_xlsx", "⚠️ Нужен файл в формате .xlsx.")
	}

	view, err := h.getOrBeginUploadView(authorTelegramID)
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
	// Show the upload session as soon as Telegram files are accepted. Parsing
	// can take noticeably longer, especially for a multi-file send.
	if err := h.sendUploadedSessionView(ctx, view); err != nil {
		return err
	}

	switch file.Status {
	case cardimport_service.FileStatusReserved:
		reader, openErr := h.bot.File(&document.File)
		if openErr != nil {
			return core_transport_telegram.Notify(ctx, "cardimport.download_failed", "❌ Не удалось скачать файл из Telegram. Отправьте его ещё раз.")
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
		return core_transport_telegram.Notify(ctx, "cardimport.file_processing", "⏳ Этот файл уже обрабатывается.")

	case cardimport_service.FileStatusValid,
		cardimport_service.FileStatusInvalid:
		return nil

	default:
		return core_transport_telegram.Notify(ctx, "cardimport.file_not_processable", "⚠️ Этот файл больше нельзя обработать.")
	}

	parsedView, err := h.service.ParseFile(h.ctx, command)
	if err != nil {
		return sendServiceError(ctx, err)
	}

	return h.sendUploadedSessionView(ctx, parsedView)
}

// getOrBeginUploadView lets a valid XLSX start a new collecting session even
// when the user sends it from another screen (for example, statistics).
func (h *Handler) getOrBeginUploadView(
	authorTelegramID int64,
) (cardimport_service.SessionView, error) {
	view, err := h.service.GetActiveView(h.ctx, authorTelegramID)
	if err == nil || !errors.Is(err, core_errors.ErrNotFound) {
		return view, err
	}

	session, err := h.service.Begin(h.ctx, cardimport_service.BeginCommand{
		AuthorTelegramID: authorTelegramID,
		Purpose:          cardimport_service.PurposeTransfer,
	})
	if err != nil {
		return cardimport_service.SessionView{}, err
	}

	return h.service.GetView(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        session.ID,
	})
}
