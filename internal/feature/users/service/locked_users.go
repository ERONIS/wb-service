package users_service

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func validateAdminTelegramID(adminTelegramID int64) error {
	if adminTelegramID <= 0 {
		return fmt.Errorf(
			"require admin: check admin: %w",
			core_errors.InvalidArgument("invalid TelegramID='%d'", adminTelegramID),
		)
	}
	return nil
}

func (s *UsersService) lockAdminAndTarget(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	adminTelegramID int64,
	targetTelegramID int64,
) (map[int64]domain.User, error) {
	users, err := s.usersRepository.LockUsersForMutation(ctx, db, adminTelegramID, targetTelegramID)
	if err != nil {
		return nil, fmt.Errorf("lock users for mutation: %w", err)
	}
	if !users[adminTelegramID].IsAdmin() {
		return nil, fmt.Errorf(
			"require admin: user with TelegramID='%d' is not an admin: %w",
			adminTelegramID,
			core_errors.ErrForbidden,
		)
	}

	return users, nil
}
