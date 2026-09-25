package core_transport_telegram

import (
	"context"
	"strings"
	"sync"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_tg_middleware "github.com/ERONIS/wb-service/internal/core/transport/telegram/middleware"

	tele "gopkg.in/telebot.v3"
	tele_middleware "gopkg.in/telebot.v3/middleware"
)

type Handler struct {
	bot        *tele.Bot
	roleAccess *core_tg_middleware.RoleAccess
	menuHost   *menuHost
	menuItems  []menuItem

	textMu       sync.RWMutex
	textHandlers map[string]inputFlow
	textOwners   map[int64]string

	documentHandlers map[string]inputFlow
	documentFallback *inputFlow
	flowCleanups     map[string]func(int64)
}

type FlowHandler func(tele.Context) (handled bool, err error)

type TextFlowHandler = FlowHandler
type DocumentFlowHandler = FlowHandler

type inputFlow struct {
	minimumRole domain.UserRole
	handler     FlowHandler
}

// Register создаёт Telegram-обработчик и регистрирует главное меню.
func Register(
	ctx context.Context,
	bot *tele.Bot,
	roleProvider core_tg_middleware.RoleProvider,
	menuStore MenuStore,
) *Handler {
	menuHost := newMenuHost(ctx, bot, menuStore)
	bot.Use(menuHost.Middleware())
	roleAccess := core_tg_middleware.NewRoleAccess(
		ctx,
		roleProvider,
	)
	handler := NewHandler(bot, roleAccess)
	handler.menuHost = menuHost
	handler.RegisterMainMenu()
	menuHost.setPersistentReplyKeyboard(
		replyKeyboardMessage,
		handler.mainMenuReplyMarkup,
	)
	menuHost.setInputFallback(
		roleAccess.Require(domain.RoleUser)(func(ctx tele.Context) error {
			return handler.renderMainMenu(ctx)
		}),
	)

	return handler
}

// RenderChat updates the tracked menu without depending on the lifetime of an
// incoming Telegram update. Background jobs use it after durable work ends.
func (h *Handler) RenderChat(
	chatID int64,
	text string,
	opts ...interface{},
) error {
	if h == nil || h.menuHost == nil || chatID == 0 {
		return tele.ErrBadContext
	}
	return h.menuHost.renderChat(chatID, text, opts...)
}

// RegisterHandler регистрирует команду или Telegram event с проверкой роли.
func (h *Handler) RegisterHandler(
	endpoint any,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
) {
	h.bot.Handle(
		endpoint,
		handler,
		h.roleAccess.Require(minimumRole),
	)
}

func NewHandler(
	bot *tele.Bot,
	roleAccess *core_tg_middleware.RoleAccess,
) *Handler {
	if bot == nil {
		panic("telegram bot is nil")
	}
	if roleAccess == nil {
		panic("telegram role access is nil")
	}

	handler := &Handler{
		bot:              bot,
		roleAccess:       roleAccess,
		menuItems:        make([]menuItem, 0),
		textHandlers:     make(map[string]inputFlow),
		textOwners:       make(map[int64]string),
		documentHandlers: make(map[string]inputFlow),
		flowCleanups:     make(map[string]func(int64)),
	}
	bot.Handle(tele.OnText, handler.handleTextFlow)
	bot.Handle(tele.OnDocument, handler.handleDocumentFlow)
	return handler
}

// RegisterFlowCleanup registers feature state cleanup for explicit flow exits
// such as returning to the main menu or starting another exclusive flow.
func (h *Handler) RegisterFlowCleanup(key string, cleanup func(int64)) {
	key = strings.TrimSpace(key)
	if key == "" || cleanup == nil {
		panic("Telegram flow cleanup registration is invalid")
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if _, exists := h.flowCleanups[key]; exists {
		panic("Telegram flow cleanup is already registered: " + key)
	}
	h.flowCleanups[key] = cleanup
}

func (h *Handler) RegisterTextFlow(
	key string,
	minimumRole domain.UserRole,
	handler TextFlowHandler,
) {
	h.registerFlow(h.textHandlers, "text", key, minimumRole, handler)
}

// RegisterDocumentFlow attaches document handling to the same owner key used
// by BeginTextFlow. This keeps multi-step text/document workflows exclusive.
func (h *Handler) RegisterDocumentFlow(
	key string,
	minimumRole domain.UserRole,
	handler DocumentFlowHandler,
) {
	h.registerFlow(h.documentHandlers, "document", key, minimumRole, handler)
}

func (h *Handler) registerFlow(
	flows map[string]inputFlow,
	kind, key string,
	minimumRole domain.UserRole,
	handler FlowHandler,
) {
	key = strings.TrimSpace(key)
	if key == "" || !minimumRole.IsValid() || handler == nil {
		panic("Telegram " + kind + " flow registration is invalid")
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if _, exists := flows[key]; exists {
		panic("Telegram " + kind + " flow is already registered: " + key)
	}
	flows[key] = inputFlow{minimumRole: minimumRole, handler: handler}
}

// RegisterDocumentFallback registers the handler used when no exclusive flow
// is active. It is intended for uploads that may begin from any screen.
func (h *Handler) RegisterDocumentFallback(
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
) {
	if !minimumRole.IsValid() || handler == nil {
		panic("Telegram document fallback registration is invalid")
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if h.documentFallback != nil {
		panic("Telegram document fallback is already registered")
	}
	h.documentFallback = &inputFlow{
		minimumRole: minimumRole,
		handler: func(ctx tele.Context) (bool, error) {
			return true, handler(ctx)
		},
	}
}

func (h *Handler) BeginTextFlow(telegramID int64, key string) {
	key = strings.TrimSpace(key)
	if telegramID <= 0 || key == "" {
		panic("Telegram text flow owner is invalid")
	}
	h.textMu.Lock()
	if _, exists := h.textHandlers[key]; !exists {
		h.textMu.Unlock()
		panic("Telegram text flow is not registered: " + key)
	}
	previous := h.textOwners[telegramID]
	h.textOwners[telegramID] = key
	cleanup := h.flowCleanups[previous]
	h.textMu.Unlock()
	if previous != "" && previous != key && cleanup != nil {
		cleanup(telegramID)
	}
}

func (h *Handler) EndTextFlow(telegramID int64, key string) {
	if telegramID <= 0 {
		return
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if current := h.textOwners[telegramID]; current == strings.TrimSpace(key) {
		delete(h.textOwners, telegramID)
	}
}

func (h *Handler) EndTextFlows(telegramID int64) {
	if telegramID <= 0 {
		return
	}
	h.textMu.Lock()
	key := h.textOwners[telegramID]
	delete(h.textOwners, telegramID)
	cleanup := h.flowCleanups[key]
	h.textMu.Unlock()
	if cleanup != nil {
		cleanup(telegramID)
	}
}

func (h *Handler) handleTextFlow(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return nil
	}
	if strings.TrimSpace(ctx.Text()) == mainMenuReplyText {
		return h.roleAccess.Require(domain.RoleUser)(h.handleMainMenuReply)(ctx)
	}
	h.textMu.RLock()
	key := h.textOwners[sender.ID]
	flow := h.textHandlers[key]
	h.textMu.RUnlock()
	if key == "" || flow.handler == nil {
		return nil
	}
	return h.executeFlow(ctx, sender.ID, key, flow)
}

func (h *Handler) handleDocumentFlow(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return nil
	}
	h.textMu.RLock()
	key := h.textOwners[sender.ID]
	flow, found := h.documentHandlers[key]
	fallback := h.documentFallback
	h.textMu.RUnlock()
	if key != "" {
		if !found || flow.handler == nil {
			return nil
		}
		return h.executeFlow(ctx, sender.ID, key, flow)
	}
	if fallback == nil || fallback.handler == nil {
		return nil
	}
	return h.executeFlow(ctx, sender.ID, "", *fallback)
}

func (h *Handler) executeFlow(
	ctx tele.Context,
	telegramID int64,
	key string,
	flow inputFlow,
) error {
	return h.roleAccess.Require(flow.minimumRole)(func(ctx tele.Context) error {
		handled, err := flow.handler(ctx)
		if err != nil {
			return err
		}
		if key != "" && !handled {
			h.EndTextFlow(telegramID, key)
			if cleanup := h.flowCleanup(key); cleanup != nil {
				cleanup(telegramID)
			}
		}
		return nil
	})(ctx)
}

func (h *Handler) flowCleanup(key string) func(int64) {
	h.textMu.RLock()
	defer h.textMu.RUnlock()
	return h.flowCleanups[strings.TrimSpace(key)]
}

func (h *Handler) HasTextFlow(telegramID int64, key string) bool {
	if h == nil || telegramID <= 0 {
		return false
	}
	h.textMu.RLock()
	defer h.textMu.RUnlock()
	return h.textOwners[telegramID] == strings.TrimSpace(key)
}

// RegisterCallback регистрирует нативный callback endpoint Telebot.
func (h *Handler) RegisterCallback(
	button tele.Btn,
	minimumRole domain.UserRole,
	handler tele.HandlerFunc,
	middlewares ...tele.MiddlewareFunc,
) {
	chain := append([]tele.MiddlewareFunc(nil), middlewares...)
	chain = append(
		chain,
		h.roleAccess.Require(minimumRole),
		tele_middleware.AutoRespond(),
	)
	h.bot.Handle(&button, handler, chain...)
}
