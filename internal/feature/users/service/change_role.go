package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (s *UsersService) SetPartner(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) (domain.User, error) {
	return s.changeRole(
		ctx,
		adminTelegramID,
		targetTelegramID,
		domain.RolePartner,
	)
}

func (s *UsersService) RevokePartner(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) (domain.User, error) {
	return s.changeRole(
		ctx,
		adminTelegramID,
		targetTelegramID,
		domain.RoleUser,
	)
}

func (s *UsersService) changeRole(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
	nextRole domain.UserRole,
) (domain.User, error) {
	if err := s.requireAdmin(ctx, adminTelegramID); err != nil {
		return domain.User{}, fmt.Errorf("require admin: %w", err)
	}
	if targetTelegramID <= 0 || !nextRole.IsValid() {
		return domain.User{}, fmt.Errorf(
			"invalid role change target TelegramID='%d', role='%s': %w",
			targetTelegramID,
			nextRole,
			core_errors.ErrInvalidArgument,
		)
	}

	target, err := s.usersRepository.GetUserByTelegramID(ctx, targetTelegramID)
	if err != nil {
		return domain.User{}, fmt.Errorf("get target user: %w", err)
	}
	if target.IsAdmin() {
		if nextRole == domain.RoleAdmin {
			return domain.User{}, fmt.Errorf(
				"user with TelegramID='%d' is already admin: %w",
				targetTelegramID,
				core_errors.ErrConflict,
			)
		}
		return domain.User{}, fmt.Errorf(
			"admin role cannot be changed for TelegramID='%d': %w",
			targetTelegramID,
			core_errors.ErrForbidden,
		)
	}
	if target.Role == nextRole {
		return domain.User{}, fmt.Errorf(
			"user with TelegramID='%d' already has role='%s': %w",
			targetTelegramID,
			nextRole,
			core_errors.ErrConflict,
		)
	}
	if err := target.ChangeRole(nextRole); err != nil {
		return domain.User{}, fmt.Errorf("change user role: %w", err)
	}

	updated, err := s.usersRepository.UpdateUser(ctx, target)
	if err != nil {
		return domain.User{}, fmt.Errorf("update user role: %w", err)
	}
	return updated, nil
}
