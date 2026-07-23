package users_service

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// IsAdmin проверяет, назначена ли пользователю роль администратора.
func (s *UsersService) IsAdmin(
	ctx context.Context,
	telegramID int64,
) (bool, error) {
	if telegramID <= 0 {
		return false, fmt.Errorf(
			"invalid TelegramID='%d': %w",
			telegramID,
			core_errors.ErrInvalidArgument,
		)
	}

	user, err := s.usersRepository.GetUserByTelegramID(
		ctx,
		telegramID,
	)
	if err != nil {
		if errors.Is(err, core_errors.ErrNotFound) {
			return false, nil
		}

		return false, fmt.Errorf(
			"get user with TelegramID='%d': %w",
			telegramID,
			err,
		)
	}

	return user.IsAdmin(), nil
}
