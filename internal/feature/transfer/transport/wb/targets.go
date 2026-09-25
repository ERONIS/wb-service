package transfer_wb_transport

import (
	"context"
	"fmt"

	"github.com/ERONIS/wb-service/internal/core/domain"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

type CabinetSource interface {
	Targets(context.Context, int64) ([]wbcabinet_service.TargetCredential, error)
	TargetsByOwnerRole(context.Context, domain.UserRole) ([]wbcabinet_service.TargetCredential, error)
	AllTargets(context.Context) ([]wbcabinet_service.TargetCredential, error)
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
) ([]transfer_service.TargetCredential, error) {
	credentials, err := transport.cabinets.Targets(ctx, ownerTelegramID)
	if err != nil {
		return nil, err
	}
	return mapCredentials(credentials), nil
}

func (transport *TargetTransport) FanoutCredentials(
	ctx context.Context,
) ([]transfer_service.TargetCredential, error) {
	roles := [...]domain.UserRole{domain.RolePartner, domain.RoleAdmin}
	credentials := make([]wbcabinet_service.TargetCredential, 0)
	for _, role := range roles {
		roleCredentials, err := transport.cabinets.TargetsByOwnerRole(ctx, role)
		if err != nil {
			return nil, fmt.Errorf("read %s WB cabinet targets: %w", role, err)
		}
		credentials = append(credentials, roleCredentials...)
	}
	return mapCredentials(credentials), nil
}

func (transport *TargetTransport) AllCredentials(
	ctx context.Context,
) ([]transfer_service.TargetCredential, error) {
	credentials, err := transport.cabinets.AllTargets(ctx)
	if err != nil {
		return nil, err
	}
	return mapCredentials(credentials), nil
}

func mapCredentials(
	credentials []wbcabinet_service.TargetCredential,
) []transfer_service.TargetCredential {
	result := make([]transfer_service.TargetCredential, len(credentials))
	for index, credential := range credentials {
		result[index] = transfer_service.TargetCredential{
			CabinetID:           transfer_service.CabinetID(credential.CabinetID),
			SellerKey:           credential.SellerKey,
			BindingRevision:     credential.BindingRevision,
			CapabilityRevision:  credential.CapabilityRevision,
			ContentRead:         credential.ContentRead,
			ContentWrite:        credential.ContentWrite,
			CredentialExpiresAt: credential.CredentialExpiresAt,
			ClientGeneration:    credential.ClientGeneration,
		}
	}
	return result
}

var _ transfer_service.TargetTransport = (*TargetTransport)(nil)
