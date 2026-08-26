package core_transport_telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

const (
	notificationVisibleFor = 4 * time.Second
	notificationDedupeFor  = 30 * time.Second
)

// MenuStore persists the active menu message across service restarts.
type MenuStore interface {
	Get(context.Context, int64) (int, bool, error)
	Save(context.Context, int64, int) error
}

type notificationState struct {
	shownAt map[string]time.Time
}

type chatState struct {
	mu           sync.Mutex
	notification notificationState
}

// menuHost serializes menu updates per chat and guarantees that only one
// tracked interactive menu message remains active.
type menuHost struct {
	ctx   context.Context
	bot   *tele.Bot
	store MenuStore
	now   func() time.Time

	statesMu sync.Mutex
	states   map[int64]*chatState
}

func newMenuHost(ctx context.Context, bot *tele.Bot, store MenuStore) *menuHost {
	if ctx == nil || bot == nil || store == nil {
		panic("Telegram menu dependency is nil")
	}
	return &menuHost{
		ctx:    ctx,
		bot:    bot,
		store:  store,
		now:    time.Now,
		states: make(map[int64]*chatState),
	}
}

// Middleware rejects callbacks from superseded menus and routes textual
// Send/Edit calls through the single active menu.
func (host *menuHost) Middleware() tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(ctx tele.Context) error {
			active, err := host.acceptCallback(ctx)
			if err != nil {
				return fmt.Errorf("validate active Telegram menu: %w", err)
			}
			if !active {
				return nil
			}
			return next(&menuContext{Context: ctx, host: host})
		}
	}
}

func (host *menuHost) acceptCallback(ctx tele.Context) (bool, error) {
	callback := ctx.Callback()
	if callback == nil || callback.Message == nil {
		return true, nil
	}
	chat := callback.Message.Chat
	if chat == nil || chat.ID == 0 || callback.Message.ID <= 0 {
		return false, ctx.RespondAlert("Кнопка устарела. Откройте последнее меню бота.")
	}

	state := host.chatState(chat.ID)
	state.mu.Lock()
	defer state.mu.Unlock()
	messageID, found, err := host.store.Get(host.ctx, chat.ID)
	if err != nil {
		return false, err
	}
	if !found {
		// Smooth rollout: the first menu used after deployment becomes active.
		if err := host.store.Save(host.ctx, chat.ID, callback.Message.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	if messageID == callback.Message.ID {
		return true, nil
	}

	// Telegram has no API for listing old chat messages. Once an old menu is
	// touched, remove it immediately so stale keyboards disappear from history.
	_ = host.bot.Delete(callback.Message)
	return false, ctx.RespondAlert("Старое меню удалено. Используйте последнее меню бота.")
}

// Notify reports an operational error without changing the active menu.
// Callback errors use a Telegram alert. Telegram cannot show callback alerts
// for message/document updates, so those errors use one temporary message that
// is deleted automatically. Repeated keys are suppressed for a short window.
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

func (host *menuHost) render(ctx tele.Context, what interface{}, opts ...interface{}) error {
	text, ok := what.(string)
	if !ok {
		return ctx.Send(what, opts...)
	}
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return tele.ErrBadContext
	}

	state := host.chatState(chat.ID)
	state.mu.Lock()
	defer state.mu.Unlock()

	messageID, found, err := host.store.Get(host.ctx, chat.ID)
	if err != nil {
		return fmt.Errorf("load active Telegram menu: %w", err)
	}
	if found {
		activeMessage := storedMessage(chat.ID, messageID)
		_, editErr := host.bot.Edit(activeMessage, text, opts...)
		if editErr == nil || sameContentError(editErr) {
			return nil
		}
		if !missingMessageError(editErr) {
			return fmt.Errorf("edit active Telegram menu: %w", editErr)
		}
		// ErrCantEditMessage may still refer to an existing message. Delete it
		// before sending a replacement so the old keyboard cannot remain active.
		if deleteErr := host.bot.Delete(activeMessage); deleteErr != nil &&
			!deletedMessageMissing(deleteErr) {
			return fmt.Errorf(
				"delete uneditable Telegram menu: edit: %v; delete: %w",
				editErr,
				deleteErr,
			)
		}
	}

	message, err := host.bot.Send(chat, text, opts...)
	if err != nil {
		return fmt.Errorf("send active Telegram menu: %w", err)
	}
	if message == nil || message.ID <= 0 {
		return errors.New("sent Telegram menu has no message ID")
	}
	if err := host.store.Save(host.ctx, chat.ID, message.ID); err != nil {
		// Do not leave an untracked second menu when persistence failed.
		_ = host.bot.Delete(message)
		return fmt.Errorf("persist active Telegram menu: %w", err)
	}
	return nil
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

	go host.deleteNotification(message)
	return nil
}

func (host *menuHost) deleteNotification(message *tele.Message) {
	timer := time.NewTimer(notificationVisibleFor)
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
	state.notification.shownAt[key] = shownAt
}

func (host *menuHost) chatState(chatID int64) *chatState {
	host.statesMu.Lock()
	defer host.statesMu.Unlock()
	state := host.states[chatID]
	if state == nil {
		state = &chatState{}
		host.states[chatID] = state
	}
	return state
}

func storedMessage(chatID int64, messageID int) tele.StoredMessage {
	return tele.StoredMessage{ChatID: chatID, MessageID: strconv.Itoa(messageID)}
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

func sameContentError(err error) bool {
	return errors.Is(err, tele.ErrMessageNotModified) ||
		errors.Is(err, tele.ErrSameMessageContent)
}

func missingMessageError(err error) bool {
	if errors.Is(err, tele.ErrCantEditMessage) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "message to edit not found") ||
		strings.Contains(message, "message to delete not found") ||
		strings.Contains(message, "message_id_invalid")
}

func deletedMessageMissing(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "message to delete not found") ||
		strings.Contains(message, "message_id_invalid")
}

type menuContext struct {
	tele.Context
	host *menuHost
}

func (ctx *menuContext) Send(what interface{}, opts ...interface{}) error {
	return ctx.host.render(ctx.Context, what, opts...)
}

func (ctx *menuContext) Edit(what interface{}, opts ...interface{}) error {
	return ctx.host.render(ctx.Context, what, opts...)
}

func (ctx *menuContext) EditOrSend(what interface{}, opts ...interface{}) error {
	return ctx.host.render(ctx.Context, what, opts...)
}

func (ctx *menuContext) NotifyMenu(key string, text string) error {
	return ctx.host.notify(ctx.Context, key, text)
}

var _ tele.Context = (*menuContext)(nil)
