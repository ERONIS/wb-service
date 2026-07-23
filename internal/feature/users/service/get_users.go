package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const usersPageSize = 20

type UsersPage struct {
	Users   []domain.User
	Page    int
	HasNext bool
}

func (s *UsersService) GetUsers(
	ctx context.Context,
	adminTelegramID int64,
	page int,
) (UsersPage, error) {
	if err := s.requireAdmin(
		ctx,
		adminTelegramID,
	); err != nil {
		return UsersPage{}, fmt.Errorf(
			"require admin: %w",
			err,
		)
	}

	if page < 1 {
		return UsersPage{}, fmt.Errorf(
			"invalid page='%d': %w",
			page,
			core_errors.ErrInvalidArgument,
		)
	}

	offset := (page - 1) * usersPageSize

	// Запрашиваем 21 пользователя.
	// Последняя запись нужна только для определения HasNext.
	users, err := s.usersRepository.GetUsers(
		ctx,
		usersPageSize+1,
		offset,
	)
	if err != nil {
		return UsersPage{}, fmt.Errorf(
			"get users from repository: %w",
			err,
		)
	}

	hasNext := len(users) > usersPageSize
	if hasNext {
		users = users[:usersPageSize]
	}

	return UsersPage{
		Users:   users,
		Page:    page,
		HasNext: hasNext,
	}, nil
}
