package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	"github.com/ERONIS/wb-service/internal/core/domain"
)

type ClientGeneration = domain.ClientGeneration

type TargetCredential struct {
	CabinetID           CabinetID
	SellerKey           SellerKey
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

type MutationTargetRegistry struct {
	transport  TargetTransport
	cohortName string
	now        func() time.Time
	// snapshot is kept as a compatibility seam for deterministic unit tests
	// and legacy fixed cohorts. Runtime construction leaves it nil.
	snapshot *MutationTargetSnapshot
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

// MutationSnapshot resolves the active verified cabinets of one Telegram
// owner. The returned snapshot is subsequently frozen in transfer_targets.
func (registry *MutationTargetRegistry) MutationSnapshot(
	ctx context.Context,
	ownerTelegramID int64,
) (MutationTargetSnapshot, error) {
	if ctx == nil {
		return MutationTargetSnapshot{}, errors.New("get mutation target snapshot: context is nil")
	}
	if ownerTelegramID <= 0 {
		return MutationTargetSnapshot{}, errors.New("get mutation target snapshot: owner is invalid")
	}
	if registry.snapshot != nil {
		return cloneMutationTargetSnapshot(*registry.snapshot), nil
	}
	credentials, err := registry.transport.Credentials(ctx, ownerTelegramID)
	if err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf("read WB target credentials: %w", err)
	}
	return registry.snapshotForCredentials(credentials)
}

// FanoutMutationSnapshot resolves all active verified cabinets whose current
// owner has the partner or admin role. The result is frozen in transfer_targets.
func (registry *MutationTargetRegistry) FanoutMutationSnapshot(
	ctx context.Context,
) (MutationTargetSnapshot, error) {
	if ctx == nil {
		return MutationTargetSnapshot{}, errors.New("get fan-out mutation target snapshot: context is nil")
	}
	if registry.snapshot != nil {
		return cloneMutationTargetSnapshot(*registry.snapshot), nil
	}
	credentials, err := registry.transport.FanoutCredentials(ctx)
	if err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf("read fan-out WB target credentials: %w", err)
	}
	return registry.snapshotForCredentials(credentials)
}

func (registry *MutationTargetRegistry) MutationSnapshotForOwner(
	ctx context.Context,
	ownerTelegramID int64,
	cabinetIDs []CabinetID,
) (MutationTargetSnapshot, error) {
	base, err := registry.MutationSnapshot(ctx, ownerTelegramID)
	if err != nil {
		return MutationTargetSnapshot{}, err
	}
	return selectMutationTargets(base, cabinetIDs, registry.now().UTC())
}

// MutationSnapshotFor preserves the fixed-cohort selection API used by the
// cabinet-copy tests. Runtime service code uses MutationSnapshotForOwner.
func (registry *MutationTargetRegistry) MutationSnapshotFor(
	ctx context.Context,
	cabinetIDs []CabinetID,
) (MutationTargetSnapshot, error) {
	if ctx == nil {
		return MutationTargetSnapshot{}, errors.New("select mutation targets: context is nil")
	}
	if registry.snapshot != nil {
		return selectMutationTargets(
			cloneMutationTargetSnapshot(*registry.snapshot),
			cabinetIDs,
			registry.now().UTC(),
		)
	}
	credentials, err := registry.transport.AllCredentials(ctx)
	if err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf("read all WB target credentials: %w", err)
	}
	base, err := registry.snapshotForCredentials(credentials)
	if err != nil {
		return MutationTargetSnapshot{}, err
	}
	return selectMutationTargets(base, cabinetIDs, registry.now().UTC())
}

// AllTargets is used only by global WB error-feed polling. Regular fan-out
// creation uses FanoutMutationSnapshot; exact cabinet-copy remains owner scoped.
func (registry *MutationTargetRegistry) AllTargets(
	ctx context.Context,
) ([]MutationTarget, error) {
	if ctx == nil {
		return nil, errors.New("get all mutation targets: context is nil")
	}
	if registry.snapshot != nil {
		return append([]MutationTarget(nil), registry.snapshot.Targets...), nil
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
	return append([]MutationTarget(nil), snapshot.Targets...), nil
}

func (registry *MutationTargetRegistry) snapshotForCredentials(
	credentials []TargetCredential,
) (MutationTargetSnapshot, error) {
	if len(credentials) == 0 {
		return MutationTargetSnapshot{}, errors.New("verify mutation target cohort: target list is empty")
	}
	targets := make([]MutationTarget, len(credentials))
	for index, credential := range credentials {
		targets[index] = MutationTarget{
			Position:            index + 1,
			CabinetID:           credential.CabinetID,
			SellerKey:           credential.SellerKey,
			ClientGeneration:    credential.ClientGeneration,
			BindingRevision:     credential.BindingRevision,
			CapabilityRevision:  credential.CapabilityRevision,
			ContentRead:         credential.ContentRead,
			ContentWrite:        credential.ContentWrite,
			CredentialExpiresAt: credential.CredentialExpiresAt,
		}
	}
	snapshot := MutationTargetSnapshot{
		CohortName: registry.cohortName,
		Revision:   targetSnapshotRevision(registry.cohortName, credentials),
		Targets:    targets,
	}
	if err := snapshot.Validate(registry.now().UTC()); err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf("validate verified mutation target snapshot: %w", err)
	}
	return snapshot, nil
}

func cloneMutationTargetSnapshot(snapshot MutationTargetSnapshot) MutationTargetSnapshot {
	result := snapshot
	result.Targets = append([]MutationTarget(nil), snapshot.Targets...)
	return result
}

func selectMutationTargets(
	base MutationTargetSnapshot,
	cabinetIDs []CabinetID,
	now time.Time,
) (MutationTargetSnapshot, error) {
	if len(cabinetIDs) == 0 {
		return MutationTargetSnapshot{}, errors.New("select mutation targets: cabinet list is empty")
	}
	byID := make(map[CabinetID]MutationTarget, len(base.Targets))
	for _, target := range base.Targets {
		byID[target.CabinetID] = target
	}
	selectedCredentials := make([]TargetCredential, 0, len(cabinetIDs))
	seen := make(map[CabinetID]struct{}, len(cabinetIDs))
	for _, cabinetID := range cabinetIDs {
		if cabinetID == "" || strings.TrimSpace(string(cabinetID)) != string(cabinetID) {
			return MutationTargetSnapshot{}, errors.New("select mutation targets: cabinet ID is invalid")
		}
		if _, duplicate := seen[cabinetID]; duplicate {
			return MutationTargetSnapshot{}, errors.New("select mutation targets: cabinet ID is duplicated")
		}
		seen[cabinetID] = struct{}{}
		target, exists := byID[cabinetID]
		if !exists {
			return MutationTargetSnapshot{}, fmt.Errorf("select mutation targets: cabinet %q is unavailable", cabinetID)
		}
		selectedCredentials = append(selectedCredentials, TargetCredential{
			CabinetID:           target.CabinetID,
			SellerKey:           target.SellerKey,
			ClientGeneration:    target.ClientGeneration,
			BindingRevision:     target.BindingRevision,
			CapabilityRevision:  target.CapabilityRevision,
			ContentRead:         target.ContentRead,
			ContentWrite:        target.ContentWrite,
			CredentialExpiresAt: target.CredentialExpiresAt,
		})
	}
	targets := make([]MutationTarget, len(selectedCredentials))
	for index, credential := range selectedCredentials {
		targets[index] = MutationTarget{
			Position:            index + 1,
			CabinetID:           credential.CabinetID,
			SellerKey:           credential.SellerKey,
			ClientGeneration:    credential.ClientGeneration,
			BindingRevision:     credential.BindingRevision,
			CapabilityRevision:  credential.CapabilityRevision,
			ContentRead:         credential.ContentRead,
			ContentWrite:        credential.ContentWrite,
			CredentialExpiresAt: credential.CredentialExpiresAt,
		}
	}
	result := MutationTargetSnapshot{
		CohortName: base.CohortName,
		Revision:   targetSnapshotRevision(base.CohortName, selectedCredentials),
		Targets:    targets,
	}
	if err := result.Validate(now); err != nil {
		return MutationTargetSnapshot{}, err
	}
	return result, nil
}

func targetSnapshotRevision(
	cohortName string,
	credentials []TargetCredential,
) Digest {
	builder := canonicalhash.NewSHA256("transfer-target-snapshot:v2")
	builder.String(cohortName)
	builder.Int64(int64(len(credentials)))
	for _, credential := range credentials {
		builder.String(string(credential.CabinetID))
		builder.Bytes(credential.SellerKey[:])
		builder.Bytes(credential.ClientGeneration[:])
		builder.Int64(credential.CredentialExpiresAt.UTC().UnixNano())
		builder.Int64(credential.BindingRevision)
		builder.Int64(credential.CapabilityRevision)
		builder.Bool(credential.ContentRead)
		builder.Bool(credential.ContentWrite)
	}
	return Digest(builder.Sum())
}

var _ TargetRegistry = (*MutationTargetRegistry)(nil)
