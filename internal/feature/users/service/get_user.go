package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) GetUser(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) (domain.User, error) {
	if err := s.requireAdmin(
		ctx,
		adminTelegramID,
	); err != nil {
		return domain.User{}, fmt.Errorf(
			"require admin: %w",
			err,
		)
	}

	if targetTelegramID <= 0 {
		return domain.User{}, fmt.Errorf(
			"invalid target TelegramID='%d': %w",
			targetTelegramID,
			core_errors.ErrInvalidArgument,
		)
	}

	user, err := s.usersRepository.GetUserByTelegramID(
		ctx,
		targetTelegramID,
	)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"get user by TelegramID='%d': %w",
			targetTelegramID,
			err,
		)
	}

	return user, nil
}
