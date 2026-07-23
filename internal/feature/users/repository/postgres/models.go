package users_postgres_repository

import (
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type rowScanner interface {
	Scan(dest ...any) error
}

// UserModel представляет строку таблицы wb.users.
type UserModel struct {
	ID int64

	TelegramID       int64
	TelegramNickname *string
	FullName         string
	Role             domain.UserRole

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (m *UserModel) Scan(row rowScanner) error {
	return row.Scan(
		&m.ID,
		&m.TelegramID,
		&m.TelegramNickname,
		&m.FullName,
		&m.Role,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
}

func modelToDomain(model UserModel) domain.User {
	return domain.NewUser(
		model.ID,
		model.TelegramID,
		model.TelegramNickname,
		model.FullName,
		model.Role,
		model.CreatedAt,
		model.UpdatedAt,
	)
}

func modelsToDomains(
	models []UserModel,
) []domain.User {
	users := make([]domain.User, len(models))

	for index, model := range models {
		users[index] = modelToDomain(model)
	}

	return users
}
