package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) GetRole(
	ctx context.Context,
	telegramID int64,
) (domain.UserRole, error) {
	if telegramID <= 0 {
		return "", fmt.Errorf(
			"invalid TelegramID='%d' : %w",
			telegramID,
			core_errors.ErrInvalidArgument,
		)
	}
	user, err := s.usersRepository.GetUserByTelegramID(
		ctx,
		telegramID,
	)
	if err != nil {
		return "", fmt.Errorf(
			"get user with TelegramID='%d': %w",
			telegramID,
			err,
		)
	}

	return user.Role, nil
}
