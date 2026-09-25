package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// GetProfile returns the user profile for a Telegram identity supplied by an
// authenticated transport.
func (s *UsersService) GetProfile(
	ctx context.Context,
	telegramID int64,
) (domain.User, error) {
	if ctx == nil || telegramID <= 0 {
		return domain.User{}, fmt.Errorf(
			"invalid profile TelegramID='%d': %w",
			telegramID,
			core_errors.ErrInvalidArgument,
		)
	}
	user, err := s.usersRepository.GetUserByTelegramID(ctx, telegramID)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"get profile with TelegramID='%d': %w",
			telegramID,
			err,
		)
	}
	return user, nil
}
