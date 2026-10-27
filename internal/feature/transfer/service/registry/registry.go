package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type MutationTargetRegistry struct {
	cabinets CabinetSource
	now      func() time.Time
}

func NewMutationTargetRegistry(cabinets CabinetSource) *MutationTargetRegistry {
	if cabinets == nil {
		panic("transfer WB cabinet source is nil")
	}
	return &MutationTargetRegistry{
		cabinets: cabinets,
		now:      time.Now,
	}
}

func (registry *MutationTargetRegistry) FanoutTargets(
	ctx context.Context,
) ([]transfer_model.MutationTarget, error) {
	if ctx == nil {
		return nil, errors.New("get fan-out mutation targets: context is nil")
	}
	credentials := make([]domain.CabinetAccess, 0)
	for _, role := range []domain.UserRole{domain.RolePartner, domain.RoleAdmin} {
		targets, err := registry.cabinets.TargetsByOwnerRole(ctx, role)
		if err != nil {
			return nil, fmt.Errorf("read %s WB targets: %w", role, err)
		}
		credentials = append(credentials, targets...)
	}
	return registry.targetsForCredentials(credentials)
}

func (registry *MutationTargetRegistry) targetsForCredentials(
	credentials []domain.CabinetAccess,
) ([]transfer_model.MutationTarget, error) {
	if len(credentials) == 0 {
		return nil, errors.New("verify mutation targets: target list is empty")
	}
	targets := make([]transfer_model.MutationTarget, len(credentials))
	for index, credential := range credentials {
		targets[index] = transfer_model.MutationTarget{
			Position:            index + 1,
			SellerID:            credential.SellerID,
			ClientGeneration:    credential.ClientGeneration,
			BindingRevision:     credential.BindingRevision,
			CapabilityRevision:  credential.CapabilityRevision,
			ContentRead:         credential.ContentRead,
			ContentWrite:        credential.ContentWrite,
			CredentialExpiresAt: credential.CredentialExpiresAt,
		}
	}
	if err := transfer_model.ValidateMutationTargets(targets, registry.now().UTC()); err != nil {
		return nil, fmt.Errorf("validate mutation targets: %w", err)
	}
	return targets, nil
}
