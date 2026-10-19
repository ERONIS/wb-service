package core_tg_menu

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

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
		if err := host.store.Save(host.ctx, chat.ID, callback.Message.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	if messageID == callback.Message.ID {
		return true, nil
	}

	_ = host.bot.Delete(callback.Message)
	return false, ctx.RespondAlert("Старое меню удалено. Используйте последнее меню бота.")
}
func (host *menuHost) inputFallback() tele.HandlerFunc {
	host.fallbackMu.RLock()
	defer host.fallbackMu.RUnlock()
	return host.fallback
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
