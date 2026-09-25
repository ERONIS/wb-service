package core_tg_middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
	tele "gopkg.in/telebot.v3"
)

// Logger logs the result of processing every incoming Telegram update.
func Logger(logger *zap.Logger) tele.MiddlewareFunc {
	if logger == nil {
		panic("telegram middleware logger is nil")
	}

	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(ctx tele.Context) error {
			startedAt := time.Now()
			err := next(ctx)

			fields := updateLogFields(ctx, time.Since(startedAt))
			if errors.Is(err, context.Canceled) {
				logger.Debug(
					"telegram update processing canceled",
					fields...,
				)
				return err
			}
			if err != nil {
				logger.Error(
					"telegram update processing failed",
					append(fields, zap.String("error", redactTelegramBotTokens(err.Error())))...,
				)
				return err
			}

			logger.Debug("telegram update processed", fields...)
			return nil
		}
	}
}

func redactTelegramBotTokens(message string) string {
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

func updateLogFields(ctx tele.Context, duration time.Duration) []zap.Field {
	update := ctx.Update()
	fields := []zap.Field{
		zap.Int("update_id", update.ID),
		zap.String("update_type", updateType(update)),
		zap.Duration("duration", duration),
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
