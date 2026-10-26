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
	key = strings.TrimSpace(key)
	if key == "" || !minimumRole.IsValid() || handler == nil {
		panic("Telegram text flow registration is invalid")
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if _, exists := h.textHandlers[key]; exists {
		panic("Telegram text flow is already registered: " + key)
	}
	h.textHandlers[key] = inputFlow{minimumRole: minimumRole, handler: handler}
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
	h.nextFlowID++
	if h.nextFlowID == 0 {
		h.nextFlowID++
	}
	h.textOwners[telegramID] = textFlowOwner{key: key, id: h.nextFlowID}
	cleanup := h.flowCleanups[previous.key]
	h.textMu.Unlock()
	if previous.key != "" && previous.key != key && cleanup != nil {
		cleanup(telegramID)
	}
}

func (h *Handler) EndTextFlow(telegramID int64, key string) {
	if telegramID <= 0 {
		return
	}
	h.textMu.Lock()
	defer h.textMu.Unlock()
	if current := h.textOwners[telegramID]; current.key == strings.TrimSpace(key) {
		delete(h.textOwners, telegramID)
	}
}

func (h *Handler) EndTextFlows(telegramID int64) {
	if telegramID <= 0 {
		return
	}
	h.textMu.Lock()
	owner := h.textOwners[telegramID]
	delete(h.textOwners, telegramID)
	cleanup := h.flowCleanups[owner.key]
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
	h.textMu.RLock()
	owner := h.textOwners[sender.ID]
	flow := h.textHandlers[owner.key]
	h.textMu.RUnlock()
	if owner.key == "" || flow.handler == nil {
		return nil
	}
	return h.executeFlow(ctx, sender.ID, owner, flow)
}

func (h *Handler) handleDocumentFlow(ctx tele.Context) error {
	sender := ctx.Sender()
	if sender == nil || sender.ID <= 0 {
		return nil
	}
	h.textMu.RLock()
	activeTextFlow := h.textOwners[sender.ID].key != ""
	fallback := h.documentFallback
	h.textMu.RUnlock()
	if activeTextFlow || fallback == nil {
		return nil
	}
	return h.executeFlow(ctx, sender.ID, textFlowOwner{}, *fallback)
}

func (h *Handler) executeFlow(
	ctx tele.Context,
	telegramID int64,
	owner textFlowOwner,
	flow inputFlow,
) error {
	return h.roleAccess.Require(flow.minimumRole)(func(ctx tele.Context) error {
		handled, err := flow.handler(ctx)
		if err != nil {
			return err
		}
		if owner.id != 0 && !handled {
			h.finishTextFlow(telegramID, owner)
		}
		return nil
	})(ctx)
}

// finishTextFlow завершает только ту сессию, которая получила сообщение.
func (h *Handler) finishTextFlow(telegramID int64, owner textFlowOwner) {
	h.textMu.Lock()
	if h.textOwners[telegramID] != owner {
		h.textMu.Unlock()
		return
	}
	delete(h.textOwners, telegramID)
	cleanup := h.flowCleanups[owner.key]
	h.textMu.Unlock()
	if cleanup != nil {
		cleanup(telegramID)
	}
}
