package users_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// IsAdmin проверяет, назначена ли пользователю роль администратора.
func (s *UsersService) IsAdmin(
	ctx context.Context,
	telegramID int64,
) (bool, error) {
	if telegramID <= 0 {
		return false, core_errors.InvalidArgument("invalid TelegramID='%d'", telegramID)
	}

	user, err := s.usersRepository.FindByTelegramID(
		ctx,
		telegramID,
	)
	if err != nil {
		if core_errors.IsNotFound(err) {
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
