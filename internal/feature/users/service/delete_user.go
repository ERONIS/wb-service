package users_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) DeleteUser(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) error {
	if err := s.requireAdmin(
		ctx,
		adminTelegramID,
	); err != nil {
		return fmt.Errorf(
			"require admin: %w",
			err,
		)
	}

	if targetTelegramID <= 0 {
		return core_errors.InvalidArgument("invalid target TelegramID='%d'", targetTelegramID)
	}

	if adminTelegramID == targetTelegramID {
		return core_errors.Forbidden("admin cannot delete himself")
	}

	target, err := s.usersRepository.GetUserByTelegramID(
		ctx,
		targetTelegramID,
	)
	if err != nil {
		return fmt.Errorf(
			"get target user: %w",
			err,
		)
	}

	if target.IsAdmin() {
		return core_errors.Forbidden("admin cannot delete another admin")
	}

	if err := s.usersRepository.DeleteUser(
		ctx,
		targetTelegramID,
	); err != nil {
		return fmt.Errorf(
			"delete user: %w",
			err,
		)
	}

	return nil
}
