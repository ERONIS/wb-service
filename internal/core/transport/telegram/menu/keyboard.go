package core_tg_menu

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

const replyKeyboardVisibleFor = time.Second

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
func SendNewMenu(ctx tele.Context, what interface{}, opts ...interface{}) error {
	if sender, ok := ctx.(interface {
		SendNewMenu(interface{}, ...interface{}) error
	}); ok {
		return sender.SendNewMenu(what, opts...)
	}
	return ctx.Send(what, opts...)
}

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
