package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
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
	if err := validateAdminTelegramID(adminTelegramID); err != nil {
		return domain.User{}, err
	}
	var updated domain.User
	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, db core_postgres_transaction.DBTX) error {
		users, err := s.lockAdminAndTarget(ctx, db, adminTelegramID, targetTelegramID)
		if err != nil {
			return err
		}
		if targetTelegramID <= 0 || !nextRole.IsValid() {
			return core_errors.InvalidArgument(
				"invalid role change target TelegramID='%d', role='%s'",
				targetTelegramID,
				nextRole,
			)
		}

		target, ok := users[targetTelegramID]
		if !ok {
			return fmt.Errorf("get target user: %w", core_errors.NotFound("user with TelegramID='%d'", targetTelegramID))
		}
		if target.IsAdmin() {
			if nextRole == domain.RoleAdmin {
				return core_errors.Conflict(
					"user with TelegramID='%d' is already admin",
					targetTelegramID,
				)
			}
			return core_errors.Forbidden(
				"admin role cannot be changed for TelegramID='%d'",
				targetTelegramID,
			)
		}
		if target.Role == nextRole {
			return core_errors.Conflict(
				"user with TelegramID='%d' already has role='%s'",
				targetTelegramID,
				nextRole,
			)
		}
		if err := target.ChangeRole(nextRole); err != nil {
			return fmt.Errorf("change user role: %w", err)
		}

		updated, err = s.usersRepository.UpdateRoleWithin(ctx, db, targetTelegramID, target.Role)
		if err != nil {
			return fmt.Errorf("update user role: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return updated, nil
}
