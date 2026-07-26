package users

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_transport_telegram "github.com/ERONIS/wb-service/internal/core/transport/telegram"
	users_postgres_repository "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"
	users_transport_tg "github.com/ERONIS/wb-service/internal/feature/users/transport/telegram"
)

// Feature объединяет зависимости модуля пользователей.
type Feature struct {
	service         *users_service.UsersService
	telegramHandler *users_transport_tg.UsersTgHandler
}

// New создаёт репозиторий, сервис и Telegram-обработчик пользователей.
func New(
	ctx context.Context,
	postgresPool core_postgres_pool.Pool,
) *Feature {
	repository := users_postgres_repository.NewUsersRepository(
		postgresPool,
	)
	service := users_service.NewUsersService(
		repository,
	)

	return &Feature{
		service: service,
		telegramHandler: users_transport_tg.NewUsersTgHandler(
			ctx,
			service,
		),
	}
}

// Service возвращает сервис пользователей для зависимых компонентов.
func (f *Feature) Service() *users_service.UsersService {
	return f.service
}

// RegisterTelegram регистрирует Telegram-команды модуля пользователей.
func (f *Feature) RegisterTelegram(
	menu *core_transport_telegram.Handler,
) {
	f.telegramHandler.Register(menu)
}
