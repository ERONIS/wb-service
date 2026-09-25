package cardedit_telegram_transport

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	cardedit_service "github.com/ERONIS/wb-service/internal/feature/cardedit/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	tele "gopkg.in/telebot.v3"
)

const flowKey = "cardedit"

var (
	buttonEdit = tele.Btn{
		Text:   "✏️ Редактировать карточку",
		Unique: "cardedit_start",
	}
	buttonConfirm = tele.Btn{
		Text:   "✅ Подтвердить",
		Unique: "cardedit_confirm",
	}
	buttonContinue = tele.Btn{
		Text:   "➡️ Продолжить",
		Unique: "cardedit_continue",
	}
	buttonCancel = tele.Btn{
		Text:   "✖️ Отменить",
		Unique: "cardedit_cancel",
	}
)

type Service interface {
	Find(context.Context, int64, string) ([]cardedit_service.Target, error)
	Submit(cardedit_service.Job) error
}

type Parser interface {
	Parse(cardimport_service.FileID, io.Reader) (cardimport_service.ParsedFile, error)
}

type Handler struct {
	ctx     context.Context
	bot     *tele.Bot
	service Service
	parser  Parser
	flowTTL time.Duration
	menu    *core_transport_telegram.Handler

	mu       sync.Mutex
	sessions map[int64]session
}

type step uint8

const (
	stepVendorCode step = iota + 1
	stepConfirm
	stepDocument
)

type session struct {
	step       step
	expiresAt  time.Time
	vendorCode string
	targets    []cardedit_service.Target
}

func New(
	ctx context.Context,
	bot *tele.Bot,
	service Service,
	parser Parser,
	flowTTL time.Duration,
) *Handler {
	if ctx == nil || bot == nil || service == nil || parser == nil || flowTTL <= 0 {
		panic("cardedit Telegram dependency is invalid")
	}
	return &Handler{
		ctx:      ctx,
		bot:      bot,
		service:  service,
		parser:   parser,
		flowTTL:  flowTTL,
		sessions: make(map[int64]session),
	}
}

func (handler *Handler) Register(menu *core_transport_telegram.Handler) {
	if menu == nil {
		panic("cardedit Telegram menu is nil")
	}
	if handler.menu != nil {
		panic("cardedit Telegram handler is already registered")
	}
	handler.menu = menu
	menu.RegisterCallback(buttonEdit, domain.RoleAdmin, handler.start)
	menu.RegisterHandler("/editcard", domain.RoleAdmin, handler.start)
	menu.RegisterCallback(buttonConfirm, domain.RoleAdmin, handler.confirm)
	menu.RegisterCallback(buttonContinue, domain.RoleAdmin, handler.continueEdit)
	menu.RegisterCallback(buttonCancel, domain.RoleAdmin, handler.cancel)
	menu.RegisterTextFlow(flowKey, domain.RoleAdmin, handler.receiveText)
	menu.RegisterDocumentFlow(flowKey, domain.RoleAdmin, handler.receiveDocument)
	menu.RegisterFlowCleanup(flowKey, handler.discardSession)
}

func MenuButton() tele.Btn { return buttonEdit }

func (handler *Handler) setSession(telegramID int64, value session) {
	handler.mu.Lock()
	handler.sessions[telegramID] = value
	handler.mu.Unlock()
}

func (handler *Handler) getSession(telegramID int64) (session, bool) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	value, found := handler.sessions[telegramID]
	if found && time.Now().After(value.expiresAt) {
		delete(handler.sessions, telegramID)
		return session{}, false
	}
	return value, found
}

func (handler *Handler) clearSession(telegramID int64) {
	handler.discardSession(telegramID)
	if handler.menu != nil {
		handler.menu.EndTextFlow(telegramID, flowKey)
	}
}

func (handler *Handler) discardSession(telegramID int64) {
	handler.mu.Lock()
	delete(handler.sessions, telegramID)
	handler.mu.Unlock()
}
