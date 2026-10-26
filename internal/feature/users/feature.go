package users

import (
	"context"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_tg_menu "github.com/ERONIS/wb-service/internal/core/transport/telegram/menu"
	users_postgres_repository "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"
	users_transport_tg "github.com/ERONIS/wb-service/internal/feature/users/transport/telegram"

	"github.com/jackc/pgx/v5"
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
	repository := users_postgres_repository.New(
		postgresPool,
	)
	service := users_service.NewUsersService(
		repository,
		core_postgres_transaction.New(postgresPool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}),
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
	menu *core_tg_menu.Handler,
) {
	f.telegramHandler.Register(menu)
}
