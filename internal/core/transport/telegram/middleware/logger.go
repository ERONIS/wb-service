package core_tg_middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

// Logger пишет успешные обновления; ошибки передаются обработчику OnError telebot.
func Logger(logger *zap.Logger) tele.MiddlewareFunc {
	if logger == nil {
		panic("telegram middleware logger is nil")
	}

	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(ctx tele.Context) error {
			startedAt := time.Now()
			if err := next(ctx); err != nil {
				return err
			}
			fields := append(updateLogFields(ctx), zap.Duration("duration", time.Since(startedAt)))
			logger.Debug("telegram update processed", fields...)
			return nil
		}
	}
}

// ErrorHandler записывает ошибки telebot один раз и скрывает токен в URL.
func ErrorHandler(logger *zap.Logger, token string) func(error, tele.Context) {
	if logger == nil {
		panic("telegram error logger is nil")
	}
	return func(err error, ctx tele.Context) {
		if err == nil {
			return
		}
		message := RedactTelegramBotTokens(err.Error())
		if token != "" {
			message = strings.ReplaceAll(message, token, "<redacted>")
		}
		fields := []zap.Field{zap.String("error", message)}
		if ctx != nil {
			fields = append(updateLogFields(ctx), fields...)
		}
		if errors.Is(err, context.Canceled) {
			logger.Debug("telegram update processing canceled", fields...)
			return
		}
		if ctx == nil {
			logger.Error("telegram bot failed", fields...)
			return
		}
		logger.Error("telegram update processing failed", fields...)
	}
}

// RedactTelegramBotTokens скрывает токен из URL Telegram API.
func RedactTelegramBotTokens(message string) string {
	const marker = "/bot"
	const replacement = "/bot<redacted>"

	searchFrom := 0
	for searchFrom < len(message) {
		markerOffset := strings.Index(message[searchFrom:], marker)
		if markerOffset < 0 {
			break
		}
		markerStart := searchFrom + markerOffset
		tokenStart := markerStart + len(marker)
		tokenEndOffset := strings.IndexByte(message[tokenStart:], '/')
		if tokenEndOffset < 0 {
			break
		}
		tokenEnd := tokenStart + tokenEndOffset
		message = message[:markerStart] + replacement + message[tokenEnd:]
		searchFrom = markerStart + len(replacement)
	}
	return message
}

func updateLogFields(ctx tele.Context) []zap.Field {
	update := ctx.Update()
	fields := []zap.Field{
		zap.Int("update_id", update.ID),
		zap.String("update_type", updateType(update)),
	}

	if sender := ctx.Sender(); sender != nil {
		fields = append(fields, zap.Int64("sender_id", sender.ID))
	}
	if message := ctx.Message(); message != nil {
		fields = append(fields, zap.Int("message_id", message.ID))
	}
	if callback := ctx.Callback(); callback != nil && callback.Unique != "" {
		fields = append(fields, zap.String("callback", callback.Unique))
	}

	return fields
}

func updateType(update tele.Update) string {
	switch {
	case update.Message != nil:
		return "message"
	case update.EditedMessage != nil:
		return "edited_message"
	case update.Callback != nil:
		return "callback_query"
	case update.InlineResult != nil:
		return "chosen_inline_result"
	default:
		return "unknown"
	}
}
