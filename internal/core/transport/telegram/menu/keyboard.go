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
	defer state.keyboardMu.Unlock()
	if state.keyboardInstalled {
		return
	}
	if err := host.sendReplyKeyboard(chat, setup.text, setup.markup()); err == nil {
		state.keyboardInstalled = true
	}
}

func SendNewMenu(ctx tele.Context, what interface{}, opts ...interface{}) error {
	if sender, ok := ctx.(interface {
		SendNewMenu(interface{}, ...interface{}) error
	}); ok {
		return sender.SendNewMenu(what, opts...)
	}
	return ctx.Send(what, opts...)
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
