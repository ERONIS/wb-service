package users_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type UsersRepository interface {
	Create(
		ctx context.Context,
		user domain.User,
	) (domain.User, error)

	FindByTelegramID(
		ctx context.Context,
		telegramID int64,
	) (domain.User, error)

	Update(
		ctx context.Context,
		user domain.User,
	) (domain.User, error)

	Delete(
		ctx context.Context,
		telegramID int64,
	) error

	List(
		ctx context.Context,
		limit int,
		offset int,
	) ([]domain.User, error)

	LockUsersForMutation(
		ctx context.Context,
		db core_postgres_transaction.DBTX,
		adminTelegramID int64,
		targetTelegramID int64,
	) (map[int64]domain.User, error)

	UpdateRoleWithin(
		ctx context.Context,
		db core_postgres_transaction.DBTX,
		telegramID int64,
		role domain.UserRole,
	) (domain.User, error)

	DeleteWithin(
		ctx context.Context,
		db core_postgres_transaction.DBTX,
		telegramID int64,
	) error
}

type UsersService struct {
	usersRepository UsersRepository
	uow             core_postgres_transaction.UnitOfWork
}

func NewUsersService(
	usersRepository UsersRepository,
	uow core_postgres_transaction.UnitOfWork,
) *UsersService {
	return &UsersService{
		usersRepository: usersRepository,
		uow:             uow,
	}
}
