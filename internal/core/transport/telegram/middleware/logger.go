package core_tg_middleware

import (
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
			if err != nil {
				logger.Error(
					"telegram update processing failed",
					append(fields, zap.Error(err))...,
				)
				return err
			}

			logger.Info("telegram update processed", fields...)
			return nil
		}
	}
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
	if chat := ctx.Chat(); chat != nil {
		fields = append(fields, zap.Int64("chat_id", chat.ID))
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
	case update.ChannelPost != nil:
		return "channel_post"
	case update.EditedChannelPost != nil:
		return "edited_channel_post"
	case update.MessageReaction != nil:
		return "message_reaction"
	case update.MessageReactionCount != nil:
		return "message_reaction_count"
	case update.Callback != nil:
		return "callback_query"
	case update.Query != nil:
		return "inline_query"
	case update.InlineResult != nil:
		return "chosen_inline_result"
	case update.ShippingQuery != nil:
		return "shipping_query"
	case update.PreCheckoutQuery != nil:
		return "pre_checkout_query"
	case update.Poll != nil:
		return "poll"
	case update.PollAnswer != nil:
		return "poll_answer"
	case update.MyChatMember != nil:
		return "my_chat_member"
	case update.ChatMember != nil:
		return "chat_member"
	case update.ChatJoinRequest != nil:
		return "chat_join_request"
	case update.Boost != nil:
		return "chat_boost"
	case update.BoostRemoved != nil:
		return "removed_chat_boost"
	default:
		return "unknown"
	}
}
