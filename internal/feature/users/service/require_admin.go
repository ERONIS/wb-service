package users_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) requireAdmin(
	ctx context.Context,
	telegramID int64,
) error {
	isAdmin, err := s.IsAdmin(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("check admin: %w", err)
	}

	if !isAdmin {
		return fmt.Errorf(
			"user with TelegramID='%d' is not an admin: %w",
			telegramID,
			core_errors.ErrForbidden,
		)
	}

	return nil
}
