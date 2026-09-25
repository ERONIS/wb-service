package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

type UserRole string

const (
	RoleAdmin   UserRole = "admin"
	RolePartner UserRole = "partner"
	RoleUser    UserRole = "user"
)

func (r UserRole) IsValid() bool {
	switch r {
	case RoleAdmin, RolePartner, RoleUser:
		return true
	default:
		return false
	}
}

type User struct {
	ID int64

	TelegramID       int64
	TelegramNickname *string
	FullName         string
	Role             UserRole

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewUser восстанавливает пользователя из данных PostgreSQL.
func NewUser(
	id int64,
	telegramID int64,
	telegramNickname *string,
	fullName string,
	role UserRole,
	createdAt time.Time,
	updatedAt time.Time,
) User {
	return User{
		ID:               id,
		TelegramID:       telegramID,
		TelegramNickname: telegramNickname,
		FullName:         fullName,
		Role:             role,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}
}

// CreateUser создаёт нового обычного пользователя.
// ID и даты позже заполнит PostgreSQL через RETURNING.
func CreateUser(
	telegramID int64,
	fullName string,
) User {
	return User{
		TelegramID: telegramID,
		FullName:   strings.TrimSpace(fullName),
		Role:       RoleUser,
	}
}

func (u User) Validate() error {
	if u.TelegramID <= 0 {
		return core_errors.InvalidArgument("invalid TelegramID '%d'", u.TelegramID)
	}

	fullNameLength := utf8.RuneCountInString(strings.TrimSpace(u.FullName))
	if fullNameLength < 3 || fullNameLength > 100 {
		return core_errors.InvalidArgument("invalid FullName length '%d'", fullNameLength)
	}

	if !u.Role.IsValid() {
		return core_errors.InvalidArgument("invalid user role '%s'", u.Role)
	}

	if u.TelegramNickname != nil {
		nickname := strings.TrimSpace(*u.TelegramNickname)
		nicknameLength := utf8.RuneCountInString(nickname)
		if nicknameLength == 0 || nicknameLength > 32 {
			return core_errors.InvalidArgument("invalid TelegramNickname length '%d'", nicknameLength)
		}
	}

	return nil
}

func (u *User) ChangeRole(role UserRole) error {
	if !role.IsValid() {
		return core_errors.InvalidArgument("invalid user role '%s'", role)
	}

	u.Role = role
	return nil
}

func (u User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

func (u User) IsPartner() bool {
	return u.Role == RolePartner
}
