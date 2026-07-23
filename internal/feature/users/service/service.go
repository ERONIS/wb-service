package users_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type UsersRepository interface {
	SaveUser(
		ctx context.Context,
		user domain.User,
	) (domain.User, error)

	GetUserByTelegramID(
		ctx context.Context,
		telegramID int64,
	) (domain.User, error)

	UpdateUser(
		ctx context.Context,
		user domain.User,
	) (domain.User, error)

	DeleteUser(
		ctx context.Context,
		telegramID int64,
	) error

	GetUsers(
		ctx context.Context,
		limit int,
		offset int,
	) ([]domain.User, error)
}

type UsersService struct {
	usersRepository UsersRepository
}

func NewUsersService(
	usersRepository UsersRepository,
) *UsersService {
	return &UsersService{
		usersRepository: usersRepository,
	}
}
