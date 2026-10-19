package core_tg_menu

import (
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"

	tele "gopkg.in/telebot.v3"
)

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
