package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) SetAdmin(
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

	target, err := s.usersRepository.GetUserByTelegramID(
		ctx,
		targetTelegramID,
	)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"get target user: %w",
			err,
		)
	}

	if target.IsAdmin() {
		return domain.User{}, fmt.Errorf(
			"user with TelegramID='%d' is already admin: %w",
			targetTelegramID,
			core_errors.ErrConflict,
		)
	}

	if err := target.ChangeRole(domain.RoleAdmin); err != nil {
		return domain.User{}, fmt.Errorf(
			"change user role: %w",
			err,
		)
	}

	updatedUser, err := s.usersRepository.UpdateUser(
		ctx,
		target,
	)
	if err != nil {
		return domain.User{}, fmt.Errorf(
			"update user: %w",
			err,
		)
	}

	return updatedUser, nil
}
