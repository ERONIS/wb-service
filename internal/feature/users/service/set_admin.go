package users_service

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

func (s *UsersService) SetAdmin(
	ctx context.Context,
	adminTelegramID int64,
	targetTelegramID int64,
) (domain.User, error) {
	return s.changeRole(
		ctx,
		adminTelegramID,
		targetTelegramID,
		domain.RoleAdmin,
	)
}
