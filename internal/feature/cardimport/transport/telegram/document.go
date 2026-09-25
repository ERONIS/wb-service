package cardimport_telegram_transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	cardimport_xlsx_transport "github.com/ERONIS/wb-service/internal/feature/cardimport/transport/xlsx"

	tele "gopkg.in/telebot.v3"
)

func (h *Handler) receiveDocument(ctx tele.Context) error {
	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
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
	chatID := int64(0)
	if chat := ctx.Chat(); chat != nil {
		chatID = chat.ID
	}
	h.enqueueRecovery(cardimportRecoveryJob{
		authorTelegramID: command.AuthorTelegramID,
		chatID:           chatID,
		file:             file,
	}, true)

	// The durable file workflow now runs independently from this Telegram
	// update. A slow menu request cannot interrupt downloading or parsing.
	return h.sendUploadedSessionView(ctx, view)
}

const staleParsingFileAge = 5 * time.Minute

func (h *Handler) resumeFiles(ctx tele.Context) error {
	authorTelegramID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return sendServiceError(ctx, err)
	}
	sessionID, err := parseSessionID(ctx.Args())
	if err != nil {
		return core_transport_telegram.Notify(ctx, "cardimport.session_argument", "⚠️ Не удалось определить текущую загрузку.")
	}

	view, err := h.service.GetView(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: authorTelegramID,
		SessionID:        sessionID,
	})
	if err != nil {
		return sendServiceError(ctx, err)
	}

	chatID := int64(0)
	if chat := ctx.Chat(); chat != nil {
		chatID = chat.ID
	}
	for _, fileView := range view.Files {
		if !isRecoverableFile(fileView.File, time.Now()) {
			continue
		}
		h.enqueueRecovery(cardimportRecoveryJob{
			authorTelegramID: authorTelegramID,
			chatID:           chatID,
			file:             fileView.File,
		}, true)
	}
	h.wakeRecoveryScheduler()
	return ctx.Respond(&tele.CallbackResponse{
		Text: "Файлы уже обрабатываются автоматически.",
	})
}

func (h *Handler) processFile(
	processCtx context.Context,
	file cardimport_service.File,
	telegramFile *tele.File,
	command cardimport_service.ParseFileCommand,
) (cardimport_service.SessionView, error) {
	switch file.Status {
	case cardimport_service.FileStatusReserved:
		if telegramFile == nil {
			telegramFile = &tele.File{
				FileID:   file.TelegramFileID,
				UniqueID: file.TelegramFileUniqueID,
				FileSize: file.DeclaredSize,
			}
		}
		reader, err := h.bot.File(telegramFile)
		if err != nil {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"download reserved Telegram file: %w",
				err,
			)
		}
		_, storeErr := h.service.StoreFile(
			processCtx,
			cardimport_service.StoreFileCommand(command),
			reader,
		)
		closeErr := reader.Close()
		if storeErr != nil {
			return cardimport_service.SessionView{}, storeErr
		}
		if closeErr != nil {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"close Telegram file: %w",
				closeErr,
			)
		}

	case cardimport_service.FileStatusStored:
		// Durable blob уже сохранён: продолжаем оборванную обработку.

	case cardimport_service.FileStatusParsing:
		if !isRecoverableFile(file, time.Now()) {
			return cardimport_service.SessionView{}, fmt.Errorf(
				"cardimport file is still being processed: %w",
				core_errors.ErrConflict,
			)
		}

	case cardimport_service.FileStatusValid,
		cardimport_service.FileStatusInvalid:
		return h.service.GetView(processCtx, cardimport_service.SessionCommand{
			AuthorTelegramID: command.AuthorTelegramID,
			SessionID:        command.SessionID,
		})

	default:
		return cardimport_service.SessionView{}, fmt.Errorf(
			"cardimport file is not processable: %w",
			core_errors.ErrConflict,
		)
	}

	return h.service.ParseFile(processCtx, command)
}

func isRecoverableFile(file cardimport_service.File, now time.Time) bool {
	switch file.Status {
	case cardimport_service.FileStatusReserved,
		cardimport_service.FileStatusStored:
		return true
	case cardimport_service.FileStatusParsing:
		return !file.UpdatedAt.After(now.Add(-staleParsingFileAge))
	default:
		return false
	}
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
