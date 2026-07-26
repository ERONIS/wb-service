package users_transport_tg

import (
	"context"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"

	tele "gopkg.in/telebot.v3"
)

const pendingAddTTL = 10 * time.Minute

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
	ctx              context.Context
	usersService     UsersService
	pendingAddMu     sync.Mutex
	pendingAdds      map[int64]pendingAddState
	nextPendingAddID uint64
}

type pendingAddState struct {
	id        uint64
	page      int
	expiresAt time.Time
	busy      bool
}

func NewUsersTgHandler(
	ctx context.Context,
	usersService UsersService,
) *UsersTgHandler {
	return &UsersTgHandler{
		ctx:          ctx,
		usersService: usersService,
		pendingAdds:  make(map[int64]pendingAddState),
	}
}

func (h *UsersTgHandler) Register(
	menu *core_transport_telegram.Handler,
) {
	menu.RegisterHandler(
		tele.OnText,
		domain.RoleUser,
		h.AddUserInput,
	)

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
