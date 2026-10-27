package core_tg_menu

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

const menuUpdateInterval = 1500 * time.Millisecond

type menuPosition struct {
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

	fresh := position.afterMessageID > 0 && previousID <= position.afterMessageID
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
		_ = host.bot.Delete(message)
		return fmt.Errorf("persist active Telegram menu: %w", err)
	}
	state.menu = snapshotMenu(text, opts)

	if found && previousID != message.ID {
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

func snapshotMenu(text string, opts []interface{}) menuSnapshot {
	return menuSnapshot{
		text: text,
		opts: append([]interface{}(nil), opts...),
	}
}
func storedMessage(chatID int64, messageID int) tele.StoredMessage {
	return tele.StoredMessage{ChatID: chatID, MessageID: strconv.Itoa(messageID)}
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
