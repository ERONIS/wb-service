package users_postgres_row

import (
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"

	"github.com/jackc/pgx/v5"
)

// Row представляет структуру строки таблицы wb.users для сканирования через pgx.
type Row struct {
	ID               int64           `db:"id"`
	TelegramID       int64           `db:"tg_id"`
	TelegramNickname *string         `db:"tg_nickname"`
	FullName         string          `db:"full_name"`
	Role             domain.UserRole `db:"role"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// Scan заполняет поля Row из переданного pgx.Row.
func (m *Row) Scan(row pgx.Row) error {
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

// ToDomain преобразует DTO строки в доменную модель пользователя.
func ToDomain(dto Row) domain.User {
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

// ToDomains преобразует срез DTO строк в срез доменных моделей.
func ToDomains(dtos []Row) []domain.User {
	users := make([]domain.User, len(dtos))
	for index, dto := range dtos {
		users[index] = ToDomain(dto)
	}
	return users
}
