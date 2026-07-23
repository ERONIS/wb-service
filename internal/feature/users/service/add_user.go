package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

func (s *UsersService) AddUser(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
	fullName string,
) (domain.User, error) {
	err := s.requireAdmin(
		ctx,
		adminTelegramID,
	)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"require admin: %w",
			err,
		)
	}

	user := domain.CreateUser(
		targetTelegramID,
		fullName,
	)

	if err := user.Validate(); err != nil {
		return domain.User{}, fmt.Errorf(
			"validate user: %w",
			err,
		)
	}

	savedUser, err := s.usersRepository.SaveUser(
		ctx,
		user,
	)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"save user: %w",
			err,
		)
	}

	return savedUser, nil
}
