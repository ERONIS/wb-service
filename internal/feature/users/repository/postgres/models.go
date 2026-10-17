package users_postgres_repository

import (
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type rowScanner interface {
	Scan(dest ...any) error
}

// userRowDTO представляет приватную структуру строки таблицы wb.users для сканирования через pgx.
type userRowDTO struct {
	ID               int64           `db:"id"`
	TelegramID       int64           `db:"tg_id"`
	TelegramNickname *string         `db:"tg_nickname"`
	FullName         string          `db:"full_name"`
	Role             domain.UserRole `db:"role"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// Scan заполняет поля userRowDTO из переданного rowScanner.
func (m *userRowDTO) Scan(row rowScanner) error {
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

// dtoToDomain преобразует DTO строки в доменную модель пользователя.
func dtoToDomain(dto userRowDTO) domain.User {
	return domain.NewUser(
		dto.ID,
		dto.TelegramID,
		dto.TelegramNickname,
		dto.FullName,
		dto.Role,
		dto.CreatedAt,
		dto.UpdatedAt,
	)
}

// dtosToDomains преобразует срез DTO строк в срез доменных моделей.
func dtosToDomains(dtos []userRowDTO) []domain.User {
	users := make([]domain.User, len(dtos))
	for index, dto := range dtos {
		users[index] = dtoToDomain(dto)
	}
	return users
}
