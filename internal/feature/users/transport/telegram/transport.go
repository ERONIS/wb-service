package users_transport_tg

import (
	"context"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"

	tele "gopkg.in/telebot.v3"
)

const pendingAddTTL = 10 * time.Minute

const addUserTextFlow = "users.add"

var (
	buttonListUsers = tele.Btn{
		Text:   "👥 Пользователи",
		Unique: "users_list",
	}
	buttonStartAddUser = tele.Btn{
		Text:   "➕ Добавить пользователя",
		Unique: "users_add_start",
	}
	buttonCancelAddUser = tele.Btn{
		Text:   "↩️ Отмена",
		Unique: "users_add_cancel",
	}
	buttonGetUser = tele.Btn{
		Unique: "users_get",
	}
	buttonSetAdmin = tele.Btn{
		Text:   "⭐ Назначить администратором",
		Unique: "users_set_admin",
	}
	buttonSetPartner = tele.Btn{
		Text:   "🤝 Назначить партнёром",
		Unique: "users_set_partner",
	}
	buttonRevokePartner = tele.Btn{
		Text:   "🚫 Отозвать роль партнёра",
		Unique: "users_revoke_partner",
	}
	buttonConfirmDeleteUser = tele.Btn{
		Text:   "🗑 Удалить",
		Unique: "users_confirm_delete",
	}
	buttonDeleteUser = tele.Btn{
		Text:   "✅ Да, удалить",
		Unique: "users_delete",
	}
)

type UsersService interface {
	GetUser(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
	) (domain.User, error)

	AddUser(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
		fullName string,
	) (domain.User, error)

	SetAdmin(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
	) (domain.User, error)

	SetPartner(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
	) (domain.User, error)

	RevokePartner(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
	) (domain.User, error)

	DeleteUser(
		ctx context.Context,
		adminTelegramID int64,
		targetTelegramID int64,
	) error

	GetUsers(
		ctx context.Context,
		adminTelegramID int64,
		page int,
	) (users_service.UsersPage, error)
}

type UsersTgHandler struct {
	ctx          context.Context
	usersService UsersService
	textFlows    *core_transport_telegram.Handler
	pendingAdds  *core_transport_telegram.StateStore[pendingAddState]
}

type pendingAddState struct {
	page int
}

func NewUsersTgHandler(
	ctx context.Context,
	usersService UsersService,
) *UsersTgHandler {
	return &UsersTgHandler{
		ctx:          ctx,
		usersService: usersService,
		pendingAdds:  core_transport_telegram.NewStateStore[pendingAddState](),
	}
}

func (h *UsersTgHandler) Register(
	menu *core_transport_telegram.Handler,
) {
	h.textFlows = menu
	menu.RegisterTextFlow(addUserTextFlow, domain.RoleAdmin, h.handleAddUserText)

	menu.RegisterMenuItem(
		buttonListUsers,
		domain.RoleAdmin,
		h.ListUsers,
	)
	menu.RegisterCallback(
		buttonStartAddUser,
		domain.RoleAdmin,
		h.StartAddUser,
	)
	menu.RegisterCallback(
		buttonCancelAddUser,
		domain.RoleAdmin,
		h.CancelAddUser,
	)
	menu.RegisterCallback(
		buttonGetUser,
		domain.RoleAdmin,
		withUserPayload(h.getUser),
	)
	menu.RegisterCallback(
		buttonSetAdmin,
		domain.RoleAdmin,
		withUserPayload(h.setAdmin),
	)
	menu.RegisterCallback(
		buttonSetPartner,
		domain.RoleAdmin,
		withUserPayload(h.setPartner),
	)
	menu.RegisterCallback(
		buttonRevokePartner,
		domain.RoleAdmin,
		withUserPayload(h.revokePartner),
	)
	menu.RegisterCallback(
		buttonConfirmDeleteUser,
		domain.RoleAdmin,
		withUserPayload(h.confirmDeleteUser),
	)
	menu.RegisterCallback(
		buttonDeleteUser,
		domain.RoleAdmin,
		withUserPayload(h.deleteUserCallback),
	)
}

func (h *UsersTgHandler) handleAddUserText(
	ctx tele.Context,
) (bool, error) {
	senderID, err := core_transport_telegram.SenderID(ctx)
	if err != nil {
		return false, nil
	}
	pending, status := h.pendingAdds.Claim(senderID)
	if status != core_transport_telegram.StateClaimed {
		return false, nil
	}
	targetID, fullName, err := parseAddUserInput(strings.Fields(ctx.Text()))
	if err != nil {
		h.finishPendingAdd(senderID, pending, true)
		return true, core_transport_telegram.Notify(ctx, "users.invalid_add_format", "⚠️ Неверный формат. Отправьте TG ID и полное имя одной строкой.")
	}
	created, err := h.createUser(ctx, senderID, targetID, fullName, pending.Value.page)
	h.finishPendingAdd(senderID, pending, !created)
	return true, err
}
