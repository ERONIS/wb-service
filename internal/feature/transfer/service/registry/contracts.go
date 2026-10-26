package registry

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

type CabinetSource interface {
	TargetsByOwnerRole(context.Context, domain.UserRole) ([]domain.CabinetAccess, error)
}
