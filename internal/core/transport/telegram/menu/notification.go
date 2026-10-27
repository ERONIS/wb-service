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
	notificationDedupeFor = 30 * time.Second
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
	if ctx.Callback() != nil {
		now := host.now().UTC()
		if duplicateNotification(state.notification, key, now) {
			state.mu.Unlock()
			return nil
		}
		responseErr := ctx.RespondAlert(text)
		if responseErr == nil {
			host.recordNotification(state, key, now)
		}
		state.mu.Unlock()
		return responseErr
	}

	base := state.menu
	notice := notificationText(text)
	opts := base.opts
	if base.text != "" {
		notice += "\n\n" + base.text
	} else {
		markup := host.bot.NewMarkup()
		markup.Inline(markup.Row(MainMenuButton()))
		opts = []interface{}{markup}
	}
	renderErr := host.renderLocked(state, chat, menuPosition{}, notice, opts...)
	state.menu = base
	state.mu.Unlock()
	if renderErr != nil {
		return fmt.Errorf("render Telegram menu notification: %w", renderErr)
	}
	if managed, ok := ctx.(*menuContext); ok {
		managed.repositioned = true
	}
	return nil
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
