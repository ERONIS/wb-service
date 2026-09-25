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
	"unicode/utf16"

	tele "gopkg.in/telebot.v3"
)

const (
	notificationVisibleFor  = 4 * time.Second
	notificationDedupeFor   = 30 * time.Second
	replyKeyboardVisibleFor = time.Second
	menuUpdateInterval      = 1500 * time.Millisecond
	// Telegram entity offsets and limits are expressed in UTF-16 code units.
	menuTextUnitLimit = 4096
)

const truncatedMenuSuffix = "\n\n<i>Часть данных скрыта из-за лимита Telegram.</i>"

// MenuStore persists the active menu message across service restarts.
type MenuStore interface {
	Get(context.Context, int64) (int, bool, error)
	Save(context.Context, int64, int) error
}

type notificationState struct {
	shownAt map[string]time.Time
}

type menuSnapshot struct {
	text string
	opts []interface{}
}

type chatState struct {
	mu                 sync.Mutex
	notification       notificationState
	menu               menuSnapshot
	lastMenuUpdate     time.Time
	keyboardMu         sync.Mutex
	keyboardInstalled  bool
	keyboardInstalling bool
}

type persistentReplyKeyboard struct {
	text   string
	markup func() *tele.ReplyMarkup
}

// menuHost serializes menu updates per chat and guarantees that only one
// tracked interactive menu message remains active.
type menuHost struct {
	ctx   context.Context
	bot   *tele.Bot
	store MenuStore
	now   func() time.Time
	wait  func(context.Context, time.Duration) error

	statesMu sync.Mutex
	states   map[int64]*chatState

	fallbackMu sync.RWMutex
	fallback   tele.HandlerFunc

	replyKeyboardMu sync.RWMutex
	replyKeyboard   persistentReplyKeyboard
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
		wait:   waitForContext,
		states: make(map[int64]*chatState),
	}
}

// Middleware rejects callbacks from superseded menus, routes textual
// Send/Edit calls through the single active menu, and keeps that menu below
// every message or document received from a user.
func (host *menuHost) Middleware() tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(ctx tele.Context) error {
			host.ensurePersistentReplyKeyboard(ctx)
			active, err := host.acceptCallback(ctx)
			if err != nil {
				return fmt.Errorf("validate active Telegram menu: %w", err)
			}
			if !active {
				return nil
			}

			managed := &menuContext{
				Context:           ctx,
				host:              host,
				repositionOnInput: isUserMessage(ctx),
				inputMessageID:    inputMessageID(ctx),
			}
			handlerErr := next(managed)
			repositionErr := managed.ensureMenuAfterInput()
			switch {
			case handlerErr != nil && repositionErr != nil:
				return errors.Join(
					handlerErr,
					fmt.Errorf("reposition Telegram menu after user input: %w", repositionErr),
				)
			case handlerErr != nil:
				return handlerErr
			default:
				return repositionErr
			}
		}
	}
}

func (host *menuHost) setPersistentReplyKeyboard(
	text string,
	markup func() *tele.ReplyMarkup,
) {
	text = strings.TrimSpace(text)
	if text == "" || markup == nil {
		panic("Telegram persistent reply keyboard is invalid")
	}
	host.replyKeyboardMu.Lock()
	defer host.replyKeyboardMu.Unlock()
	if host.replyKeyboard.markup != nil {
		panic("Telegram persistent reply keyboard is already configured")
	}
	host.replyKeyboard = persistentReplyKeyboard{text: text, markup: markup}
}

func (host *menuHost) persistentReplyKeyboard() persistentReplyKeyboard {
	host.replyKeyboardMu.RLock()
	defer host.replyKeyboardMu.RUnlock()
	return host.replyKeyboard
}

// ensurePersistentReplyKeyboard restores the keyboard once per chat after a
// process restart. It runs independently so Telegram latency cannot hold up
// normal update handling.
func (host *menuHost) ensurePersistentReplyKeyboard(ctx tele.Context) {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return
	}
	setup := host.persistentReplyKeyboard()
	if setup.markup == nil {
		return
	}

	state := host.chatState(chat.ID)
	state.keyboardMu.Lock()
	if state.keyboardInstalled || state.keyboardInstalling {
		state.keyboardMu.Unlock()
		return
	}
	state.keyboardInstalling = true
	state.keyboardMu.Unlock()

	go func(chatID int64) {
		err := host.sendReplyKeyboard(
			&tele.Chat{ID: chatID},
			setup.text,
			setup.markup(),
		)
		state.keyboardMu.Lock()
		state.keyboardInstalling = false
		state.keyboardInstalled = err == nil
		state.keyboardMu.Unlock()
	}(chat.ID)
}

func (host *menuHost) setInputFallback(handler tele.HandlerFunc) {
	if handler == nil {
		panic("Telegram input menu fallback is nil")
	}
	host.fallbackMu.Lock()
	defer host.fallbackMu.Unlock()
	if host.fallback != nil {
		panic("Telegram input menu fallback is already registered")
	}
	host.fallback = handler
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

// menuPosition either keeps the active menu, moves it below an input message,
// or forces a new message. Its zero value keeps editing the active menu.
type menuPosition struct {
	fresh          bool
	afterMessageID int
}

func (host *menuHost) render(
	ctx tele.Context,
	position menuPosition,
	text string,
	opts ...interface{},
) error {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return tele.ErrBadContext
	}
	state := host.chatState(chat.ID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return host.renderLocked(state, chat, position, text, opts...)
}

func (host *menuHost) renderChat(
	chatID int64,
	text string,
	opts ...interface{},
) error {
	if chatID == 0 {
		return tele.ErrBadContext
	}
	state := host.chatState(chatID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return host.renderLocked(
		state,
		&tele.Chat{ID: chatID},
		menuPosition{},
		text,
		opts...,
	)
}

func (host *menuHost) renderLocked(
	state *chatState,
	chat *tele.Chat,
	position menuPosition,
	text string,
	opts ...interface{},
) error {
	text = limitMenuText(text)

	previousID, found, err := host.store.Get(host.ctx, chat.ID)
	if err != nil {
		return fmt.Errorf("load active Telegram menu: %w", err)
	}
	if err := host.startMenuUpdateLocked(state); err != nil {
		return err
	}

	fresh := position.fresh ||
		(position.afterMessageID > 0 && previousID <= position.afterMessageID)
	if found && !fresh {
		active := storedMessage(chat.ID, previousID)
		_, editErr := host.bot.Edit(active, text, opts...)
		if editErr == nil || sameContentError(editErr) {
			state.menu = snapshotMenu(text, opts)
			return nil
		}
		if !missingMessageError(editErr) {
			return fmt.Errorf("edit active Telegram menu: %w", editErr)
		}
		// An uneditable message can still exist. Remove it before replacing it
		// so we cannot leave another interactive keyboard behind.
		if deleteErr := host.bot.Delete(active); deleteErr != nil &&
			!deletedMessageMissing(deleteErr) {
			return fmt.Errorf(
				"delete uneditable Telegram menu: edit: %v; delete: %w",
				editErr, deleteErr,
			)
		}
		found = false
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
	state.menu = snapshotMenu(text, opts)

	if found && previousID != message.ID {
		// Save the replacement first: old callbacks become stale even if
		// Telegram can no longer delete the previous message.
		_ = host.bot.Delete(storedMessage(chat.ID, previousID))
	}
	return nil
}

func (host *menuHost) renderCached(ctx tele.Context, position menuPosition) (bool, error) {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return false, tele.ErrBadContext
	}
	state := host.chatState(chat.ID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.menu.text == "" {
		return false, nil
	}
	err := host.renderLocked(state, chat, position, state.menu.text, state.menu.opts...)
	return err == nil, err
}

func (host *menuHost) startMenuUpdateLocked(state *chatState) error {
	now := host.now()
	if !state.lastMenuUpdate.IsZero() {
		remaining := menuUpdateInterval - now.Sub(state.lastMenuUpdate)
		if remaining > 0 {
			if err := host.wait(host.ctx, remaining); err != nil {
				return fmt.Errorf("wait for Telegram menu update limit: %w", err)
			}
		}
	}
	state.lastMenuUpdate = host.now()
	return nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (host *menuHost) inputFallback() tele.HandlerFunc {
	host.fallbackMu.RLock()
	defer host.fallbackMu.RUnlock()
	return host.fallback
}

func snapshotMenu(text string, opts []interface{}) menuSnapshot {
	return menuSnapshot{
		text: text,
		opts: append([]interface{}(nil), opts...),
	}
}

func isUserMessage(ctx tele.Context) bool {
	if ctx == nil {
		return false
	}
	message := ctx.Update().Message
	return message != nil && message.Sender != nil && !message.Sender.IsBot
}

func inputMessageID(ctx tele.Context) int {
	if !isUserMessage(ctx) {
		return 0
	}
	return ctx.Update().Message.ID
}

// SendNewMenu requests a newly positioned active menu when the context is
// managed by menuHost. Plain Telebot contexts fall back to a regular send.
func SendNewMenu(ctx tele.Context, what interface{}, opts ...interface{}) error {
	if sender, ok := ctx.(interface {
		SendNewMenu(interface{}, ...interface{}) error
	}); ok {
		return sender.SendNewMenu(what, opts...)
	}
	return ctx.Send(what, opts...)
}

// InstallReplyKeyboard installs a persistent keyboard without turning its
// short-lived setup message into the tracked interactive menu.
func InstallReplyKeyboard(
	ctx tele.Context,
	text string,
	markup *tele.ReplyMarkup,
) error {
	if installer, ok := ctx.(interface {
		InstallReplyKeyboard(string, *tele.ReplyMarkup) error
	}); ok {
		return installer.InstallReplyKeyboard(text, markup)
	}
	return ctx.Send(text, markup)
}

func (host *menuHost) installReplyKeyboard(
	ctx tele.Context,
	text string,
	markup *tele.ReplyMarkup,
) error {
	chat := ctx.Chat()
	if chat == nil || chat.ID == 0 {
		return tele.ErrBadContext
	}
	if err := host.sendReplyKeyboard(chat, text, markup); err != nil {
		return err
	}
	state := host.chatState(chat.ID)
	state.keyboardMu.Lock()
	state.keyboardInstalled = true
	state.keyboardInstalling = false
	state.keyboardMu.Unlock()
	return nil
}

func (host *menuHost) sendReplyKeyboard(
	chat *tele.Chat,
	text string,
	markup *tele.ReplyMarkup,
) error {
	if chat == nil || chat.ID == 0 || markup == nil {
		return tele.ErrBadContext
	}
	message, err := host.bot.Send(chat, text, markup)
	if err != nil {
		return fmt.Errorf("send Telegram reply keyboard: %w", err)
	}
	if message == nil || message.ID <= 0 {
		return errors.New("Telegram reply keyboard message has no message ID")
	}
	go host.deleteAfter(message, replyKeyboardVisibleFor)
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

// limitMenuText guarantees that generated HTML menus fit Telegram's message
// limit. Oversized content is converted to escaped plain text before it is
// shortened, so truncation cannot leave an HTML tag or entity incomplete.
func limitMenuText(text string) string {
	plainText := plainTextFromMenuHTML(text)
	if telegramTextUnits(plainText) <= menuTextUnitLimit {
		return text
	}

	suffixUnits := telegramTextUnits(plainTextFromMenuHTML(truncatedMenuSuffix))
	plainText = truncateTelegramText(
		strings.TrimSpace(plainText),
		menuTextUnitLimit-suffixUnits,
	)
	return html.EscapeString(plainText) + truncatedMenuSuffix
}

func truncateTelegramText(text string, maximumUnits int) string {
	if maximumUnits <= 0 {
		return ""
	}
	if telegramTextUnits(text) <= maximumUnits {
		return text
	}

	const ellipsis = '…'
	remaining := maximumUnits - utf16.RuneLen(ellipsis)
	var builder strings.Builder
	for _, current := range text {
		units := utf16.RuneLen(current)
		if units < 1 {
			units = 1
		}
		if units > remaining {
			break
		}
		builder.WriteRune(current)
		remaining -= units
	}
	builder.WriteRune(ellipsis)
	return builder.String()
}

func telegramTextUnits(text string) int {
	units := 0
	for _, current := range text {
		count := utf16.RuneLen(current)
		if count < 1 {
			count = 1
		}
		units += count
	}
	return units
}

func plainTextFromMenuHTML(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	inTag := false
	for _, current := range text {
		switch current {
		case '<':
			inTag = true
		case '>':
			if inTag {
				inTag = false
				continue
			}
			builder.WriteRune(current)
		default:
			if !inTag {
				builder.WriteRune(current)
			}
		}
	}
	return html.UnescapeString(builder.String())
}

func sameContentError(err error) bool {
	return errors.Is(err, tele.ErrMessageNotModified) ||
		errors.Is(err, tele.ErrSameMessageContent)
}

func missingMessageError(err error) bool {
	if errors.Is(err, tele.ErrCantEditMessage) || deletedMessageMissing(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "message to edit not found")
}

func deletedMessageMissing(err error) bool {
	if err == nil {
		return true
	}
	message := strings.ToLower(err.Error())
	return errors.Is(err, tele.ErrNotFoundToDelete) ||
		errors.Is(err, tele.ErrNoRightsToDelete) ||
		strings.Contains(message, "message to delete not found") ||
		strings.Contains(message, "message can't be deleted") ||
		strings.Contains(message, "message_id_invalid")
}

type menuContext struct {
	tele.Context
	host              *menuHost
	repositionOnInput bool
	inputMessageID    int
	inputDeleted      bool
	repositioned      bool
}

func (ctx *menuContext) Send(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) Edit(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) EditOrSend(what interface{}, opts ...interface{}) error {
	return ctx.render(what, opts...)
}

func (ctx *menuContext) Delete() error {
	err := ctx.Context.Delete()
	if err == nil && ctx.repositionOnInput {
		ctx.inputDeleted = true
	}
	return err
}

func (ctx *menuContext) SendNewMenu(what interface{}, opts ...interface{}) error {
	text, ok := what.(string)
	if !ok {
		return ctx.Context.Send(what, opts...)
	}
	err := ctx.host.render(ctx.Context, menuPosition{fresh: true}, text, opts...)
	if err == nil {
		ctx.repositioned = true
	}
	return err
}

func (ctx *menuContext) InstallReplyKeyboard(
	text string,
	markup *tele.ReplyMarkup,
) error {
	return ctx.host.installReplyKeyboard(ctx.Context, text, markup)
}

func (ctx *menuContext) NotifyMenu(key string, text string) error {
	return ctx.host.notify(ctx.Context, key, text)
}

func (ctx *menuContext) position() menuPosition {
	if ctx.repositionOnInput && !ctx.repositioned && !ctx.inputDeleted {
		return menuPosition{afterMessageID: ctx.inputMessageID}
	}
	return menuPosition{}
}

func (ctx *menuContext) render(what interface{}, opts ...interface{}) error {
	text, ok := what.(string)
	if !ok {
		return ctx.Context.Send(what, opts...)
	}
	err := ctx.host.render(ctx.Context, ctx.position(), text, opts...)
	if err == nil {
		ctx.repositioned = true
	}
	return err
}

func (ctx *menuContext) ensureMenuAfterInput() error {
	if !ctx.repositionOnInput || ctx.repositioned {
		return nil
	}

	repositioned, err := ctx.host.renderCached(ctx.Context, ctx.position())
	if err != nil {
		return err
	}
	if repositioned {
		ctx.repositioned = true
		return nil
	}

	fallback := ctx.host.inputFallback()
	if fallback == nil {
		return nil
	}
	if err := fallback(ctx); err != nil {
		return err
	}
	ctx.repositioned = true
	return nil
}

var _ tele.Context = (*menuContext)(nil)
