package core_tg_menu

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

const (
	notificationVisibleFor = 4 * time.Second
	notificationDedupeFor  = 30 * time.Second
)

func Notify(ctx tele.Context, key string, text string) error {
	if notifier, ok := ctx.(interface {
		NotifyMenu(string, string) error
	}); ok {
		return notifier.NotifyMenu(key, text)
	}
	if ctx.Callback() != nil {
		return ctx.RespondAlert(strings.TrimSpace(text))
	}
	return ctx.Send(notificationText(text))
}
func (host *menuHost) notify(ctx tele.Context, key string, text string) error {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return tele.ErrBadContext
	}
	text = strings.TrimSpace(text)
	key = normalizedNotificationKey(key, text)
	if text == "" {
		return errors.New("Telegram menu notification is empty")
	}

	state := host.chatState(chat.ID)
	state.mu.Lock()
	now := host.now().UTC()
	if duplicateNotification(state.notification, key, now) {
		state.mu.Unlock()
		return nil
	}

	if ctx.Callback() != nil {
		responseErr := ctx.RespondAlert(text)
		if responseErr == nil {
			host.recordNotification(state, key, now)
		}
		state.mu.Unlock()
		return responseErr
	}

	message, sendErr := host.bot.Send(chat, notificationText(text))
	if sendErr != nil {
		state.mu.Unlock()
		return fmt.Errorf("send temporary Telegram notification: %w", sendErr)
	}
	if message == nil || message.ID <= 0 {
		state.mu.Unlock()
		return errors.New("temporary Telegram notification has no message ID")
	}
	host.recordNotification(state, key, now)
	state.mu.Unlock()

	go host.deleteAfter(message, notificationVisibleFor)
	return nil
}

func (host *menuHost) deleteAfter(message *tele.Message, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-host.ctx.Done():
		return
	case <-timer.C:
	}
	_ = host.bot.Delete(message)
}

func (host *menuHost) recordNotification(
	state *chatState,
	key string,
	shownAt time.Time,
) {
	if state.notification.shownAt == nil {
		state.notification.shownAt = make(map[string]time.Time)
	}
	for previousKey, previousTime := range state.notification.shownAt {
		age := shownAt.Sub(previousTime)
		if age < 0 || age >= notificationDedupeFor {
			delete(state.notification.shownAt, previousKey)
		}
	}
	state.notification.shownAt[key] = shownAt
}

func normalizedNotificationKey(key string, text string) string {
	key = strings.TrimSpace(key)
	if key != "" {
		return key
	}
	return strings.ToLower(strings.TrimSpace(text))
}

func duplicateNotification(previous notificationState, key string, now time.Time) bool {
	shownAt := previous.shownAt[key]
	return !shownAt.IsZero() && now.Sub(shownAt) >= 0 &&
		now.Sub(shownAt) < notificationDedupeFor
}

func notificationText(notification string) string {
	return html.EscapeString(strings.TrimSpace(notification))
}
