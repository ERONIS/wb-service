package transfer_wb_transport

import (
	"context"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/registry"
)

type CabinetSource interface {
	Targets(context.Context, int64) ([]domain.CabinetAccess, error)
	TargetsByOwnerRole(context.Context, domain.UserRole) ([]domain.CabinetAccess, error)
	AllTargets(context.Context) ([]domain.CabinetAccess, error)
}

type TargetTransport struct {
	cabinets CabinetSource
}

func NewTargetTransport(cabinets CabinetSource) *TargetTransport {
	if cabinets == nil {
		panic("transfer WB cabinet source is nil")
	}
	return &TargetTransport{cabinets: cabinets}
}

func (transport *TargetTransport) Credentials(
	ctx context.Context,
	ownerTelegramID int64,
) ([]domain.CabinetAccess, error) {
	credentials, err := transport.cabinets.Targets(ctx, ownerTelegramID)
	if err != nil {
		return nil, err
	}
	return credentials, nil
}

func (transport *TargetTransport) CredentialsByOwnerRole(ctx context.Context, role domain.UserRole) ([]domain.CabinetAccess, error) {
	return transport.cabinets.TargetsByOwnerRole(ctx, role)
}

func (transport *TargetTransport) AllCredentials(
	ctx context.Context,
) ([]domain.CabinetAccess, error) {
	credentials, err := transport.cabinets.AllTargets(ctx)
	if err != nil {
		return nil, err
	}
	return credentials, nil
}

var _ registry.TargetTransport = (*TargetTransport)(nil)
