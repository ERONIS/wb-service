package wbcabinet_telegram_transport

import (
	"context"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	tele "gopkg.in/telebot.v3"
)

const (
	addCabinetTextFlow = "wbcabinet.add"
	addCabinetTTL      = 10 * time.Minute
)

const (
	stepCabinetName addStep = iota + 1
	stepCabinetToken
)

var (
	buttonCabinets     = tele.Btn{Text: "👤 Профиль", Unique: "wbcabinet_menu"}
	buttonAdd          = tele.Btn{Text: "➕ Добавить кабинет", Unique: "wbcabinet_add"}
	buttonCancel       = tele.Btn{Text: "↩️ Отмена", Unique: "wbcabinet_cancel"}
	buttonDeleteSelect = tele.Btn{Text: "🗑 Удалить кабинет", Unique: "wbcabinet_del"}
	buttonDeletePick   = tele.Btn{Unique: "wbcabinet_del_pick"}
	buttonDelete       = tele.Btn{Text: "✅ Да, удалить", Unique: "wbcabinet_del_ok"}
)

type CabinetService interface {
	Add(context.Context, wbcabinet_service.AddCommand) (wbcabinet_service.Cabinet, error)
	ListByOwner(context.Context, int64) ([]wbcabinet_service.Cabinet, error)
	Delete(context.Context, int64, wbcabinet_service.CabinetID) error
}

type ProfileProvider interface {
	GetProfile(context.Context, int64) (domain.User, error)
}

type Handler struct {
	ctx       context.Context
	service   CabinetService
	profiles  ProfileProvider
	textFlows *core_transport_telegram.Handler
	pending   *core_transport_telegram.StateStore[pendingAdd]
}

type addStep uint8

type pendingAdd struct {
	step addStep
	name string
}

func New(ctx context.Context, service CabinetService) *Handler {
	if ctx == nil || service == nil {
		panic("wbcabinet Telegram dependency is nil")
	}
	return &Handler{
		ctx:     ctx,
		service: service,
		pending: core_transport_telegram.NewStateStore[pendingAdd](),
	}
}

func (handler *Handler) Register(
	menu *core_transport_telegram.Handler,
	profiles ProfileProvider,
) {
	if menu == nil || profiles == nil {
		panic("wbcabinet Telegram dependency is nil")
	}
	handler.profiles = profiles
	handler.textFlows = menu
	menu.RegisterTextFlow(addCabinetTextFlow, domain.RoleUser, handler.handleText)
	menu.RegisterMenuItem(buttonCabinets, domain.RoleUser, handler.list)
	menu.RegisterCallback(buttonAdd, domain.RoleUser, handler.startAdd)
	menu.RegisterCallback(buttonCancel, domain.RoleUser, handler.cancelAdd)
	menu.RegisterCallback(buttonDeleteSelect, domain.RoleUser, handler.startDelete)
	menu.RegisterCallback(buttonDeletePick, domain.RoleUser, handler.confirmDelete)
	menu.RegisterCallback(buttonDelete, domain.RoleUser, handler.deleteCabinet)
}
