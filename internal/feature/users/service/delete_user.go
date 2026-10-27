package users_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func (s *UsersService) DeleteUser(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) error {
	if err := validateAdminTelegramID(adminTelegramID); err != nil {
		return err
	}
	return s.uow.WithinTransaction(ctx, func(ctx context.Context, db core_postgres_transaction.DBTX) error {
		users, err := s.lockAdminAndTarget(ctx, db, adminTelegramID, targetTelegramID)
		if err != nil {
			return err
		}
		if targetTelegramID <= 0 {
			return core_errors.InvalidArgument("invalid target TelegramID='%d'", targetTelegramID)
		}
		if adminTelegramID == targetTelegramID {
			return core_errors.Forbidden("admin cannot delete himself")
		}

		target, ok := users[targetTelegramID]
		if !ok {
			return fmt.Errorf("get target user: %w", core_errors.NotFound("user with TelegramID='%d'", targetTelegramID))
		}
		if target.IsAdmin() {
			return core_errors.Forbidden("admin cannot delete another admin")
		}
		if err := s.usersRepository.DeleteWithin(ctx, db, targetTelegramID); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}
