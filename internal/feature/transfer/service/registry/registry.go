package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type MutationTargetRegistry struct {
	transport  TargetTransport
	cohortName string
	now        func() time.Time
}

func NewMutationTargetRegistry(
	transport TargetTransport,
	cohortName string,
) *MutationTargetRegistry {
	if transport == nil {
		panic("transfer WB target transport is nil")
	}
	if strings.TrimSpace(cohortName) != cohortName || cohortName == "" ||
		len(cohortName) > 128 {
		panic("transfer target cohort name is invalid")
	}
	return &MutationTargetRegistry{
		transport:  transport,
		cohortName: cohortName,
		now:        time.Now,
	}
}

func (registry *MutationTargetRegistry) MutationSnapshot(
	ctx context.Context,
	ownerTelegramID int64,
) (transfer_model.MutationTargetSnapshot, error) {
	if ctx == nil {
		return transfer_model.MutationTargetSnapshot{}, errors.New("get mutation target snapshot: context is nil")
	}
	if ownerTelegramID <= 0 {
		return transfer_model.MutationTargetSnapshot{}, errors.New("get mutation target snapshot: owner is invalid")
	}
	credentials, err := registry.transport.Credentials(ctx, ownerTelegramID)
	if err != nil {
		return transfer_model.MutationTargetSnapshot{}, fmt.Errorf("read WB target credentials: %w", err)
	}
	return registry.snapshotForCredentials(credentials)
}

func (registry *MutationTargetRegistry) FanoutMutationSnapshot(
	ctx context.Context,
) (transfer_model.MutationTargetSnapshot, error) {
	if ctx == nil {
		return transfer_model.MutationTargetSnapshot{}, errors.New("get fan-out mutation target snapshot: context is nil")
	}
	credentials := make([]domain.CabinetAccess, 0)
	for _, role := range []domain.UserRole{domain.RolePartner, domain.RoleAdmin} {
		targets, err := registry.transport.CredentialsByOwnerRole(ctx, role)
		if err != nil {
			return transfer_model.MutationTargetSnapshot{}, fmt.Errorf("read %s WB targets: %w", role, err)
		}
		credentials = append(credentials, targets...)
	}
	return registry.snapshotForCredentials(credentials)
}

func (registry *MutationTargetRegistry) MutationSnapshotForOwner(
	ctx context.Context,
	ownerTelegramID int64,
	sellerIDs []domain.SellerID,
) (transfer_model.MutationTargetSnapshot, error) {
	base, err := registry.MutationSnapshot(ctx, ownerTelegramID)
	if err != nil {
		return transfer_model.MutationTargetSnapshot{}, err
	}
	return selectMutationTargets(base, sellerIDs, registry.now().UTC())
}

func (registry *MutationTargetRegistry) MutationSnapshotFor(
	ctx context.Context,
	sellerIDs []domain.SellerID,
) (transfer_model.MutationTargetSnapshot, error) {
	if ctx == nil {
		return transfer_model.MutationTargetSnapshot{}, errors.New("select mutation targets: context is nil")
	}
	credentials, err := registry.transport.AllCredentials(ctx)
	if err != nil {
		return transfer_model.MutationTargetSnapshot{}, fmt.Errorf("read all WB target credentials: %w", err)
	}
	base, err := registry.snapshotForCredentials(credentials)
	if err != nil {
		return transfer_model.MutationTargetSnapshot{}, err
	}
	return selectMutationTargets(base, sellerIDs, registry.now().UTC())
}

func (registry *MutationTargetRegistry) AllTargets(
	ctx context.Context,
) ([]transfer_model.MutationTarget, error) {
	if ctx == nil {
		return nil, errors.New("get all mutation targets: context is nil")
	}
	credentials, err := registry.transport.AllCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("read all WB target credentials: %w", err)
	}
	if len(credentials) == 0 {
		return nil, nil
	}
	snapshot, err := registry.snapshotForCredentials(credentials)
	if err != nil {
		return nil, err
	}
	return append([]transfer_model.MutationTarget(nil), snapshot.Targets...), nil
}

func (registry *MutationTargetRegistry) snapshotForCredentials(
	credentials []domain.CabinetAccess,
) (transfer_model.MutationTargetSnapshot, error) {
	if len(credentials) == 0 {
		return transfer_model.MutationTargetSnapshot{}, errors.New("verify mutation target cohort: target list is empty")
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
	snapshot := transfer_model.MutationTargetSnapshot{
		CohortName: registry.cohortName,
		Targets:    targets,
	}
	if err := snapshot.Validate(registry.now().UTC()); err != nil {
		return transfer_model.MutationTargetSnapshot{}, fmt.Errorf("validate verified mutation target snapshot: %w", err)
	}
	return snapshot, nil
}

func selectMutationTargets(
	base transfer_model.MutationTargetSnapshot,
	sellerIDs []domain.SellerID,
	now time.Time,
) (transfer_model.MutationTargetSnapshot, error) {
	if len(sellerIDs) == 0 {
		return transfer_model.MutationTargetSnapshot{}, errors.New("select mutation targets: cabinet list is empty")
	}
	byID := make(map[domain.SellerID]transfer_model.MutationTarget, len(base.Targets))
	for _, target := range base.Targets {
		byID[target.SellerID] = target
	}
	selectedCredentials := make([]domain.CabinetAccess, 0, len(sellerIDs))
	seen := make(map[domain.SellerID]struct{}, len(sellerIDs))
	for _, sellerID := range sellerIDs {
		if sellerID.Validate() != nil {
			return transfer_model.MutationTargetSnapshot{}, errors.New("select mutation targets: seller ID is invalid")
		}
		if _, duplicate := seen[sellerID]; duplicate {
			return transfer_model.MutationTargetSnapshot{}, errors.New("select mutation targets: seller ID is duplicated")
		}
		seen[sellerID] = struct{}{}
		target, exists := byID[sellerID]
		if !exists {
			return transfer_model.MutationTargetSnapshot{}, fmt.Errorf("select mutation targets: cabinet %q is unavailable", sellerID)
		}
		selectedCredentials = append(selectedCredentials, domain.CabinetAccess{
			SellerID:            target.SellerID,
			ClientGeneration:    target.ClientGeneration,
			BindingRevision:     target.BindingRevision,
			CapabilityRevision:  target.CapabilityRevision,
			ContentRead:         target.ContentRead,
			ContentWrite:        target.ContentWrite,
			CredentialExpiresAt: target.CredentialExpiresAt,
		})
	}
	targets := make([]transfer_model.MutationTarget, len(selectedCredentials))
	for index, credential := range selectedCredentials {
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
	result := transfer_model.MutationTargetSnapshot{
		CohortName: base.CohortName,
		Targets:    targets,
	}
	if err := result.Validate(now); err != nil {
		return transfer_model.MutationTargetSnapshot{}, err
	}
	return result, nil
}

var _ TargetRegistry = (*MutationTargetRegistry)(nil)
