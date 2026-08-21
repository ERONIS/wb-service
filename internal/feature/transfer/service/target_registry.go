package transfer_service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

var ErrTargetIdentityMismatch = errors.New(
	"WB mutation target identity mismatch",
)
var ErrDuplicateTargetSeller = errors.New(
	"WB mutation target seller is duplicated",
)

type ClientGeneration [sha256.Size]byte

type TargetCredential struct {
	CabinetID           CabinetID
	SellerKey           Digest
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

type VerifiedTarget TargetCredential

type TargetBinding struct {
	CabinetID           CabinetID
	SellerKey           Digest
	BindingRevision     int64
	CapabilityRevision  int64
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}

type MutationTargetRegistry struct {
	transport  TargetTransport
	store      TargetBindingStore
	cohortName string
	now        func() time.Time
	snapshot   *MutationTargetSnapshot
}

func NewMutationTargetRegistry(
	transport TargetTransport,
	store TargetBindingStore,
	cohortName string,
) *MutationTargetRegistry {
	if transport == nil {
		panic("transfer WB target transport is nil")
	}
	if store == nil {
		panic("transfer target binding store is nil")
	}
	if strings.TrimSpace(cohortName) != cohortName || cohortName == "" ||
		len(cohortName) > 128 {
		panic("transfer target cohort name is invalid")
	}
	return &MutationTargetRegistry{
		transport:  transport,
		store:      store,
		cohortName: cohortName,
		now:        time.Now,
	}
}

func (registry *MutationTargetRegistry) VerifyAtStartup(
	ctx context.Context,
) error {
	if registry.snapshot != nil {
		return errors.New("verify mutation target cohort: already verified")
	}
	snapshot, err := registry.verifySnapshot(ctx)
	if err != nil {
		return err
	}
	frozen := cloneMutationTargetSnapshot(snapshot)
	registry.snapshot = &frozen
	return nil
}

func (registry *MutationTargetRegistry) MutationSnapshot(
	ctx context.Context,
) (MutationTargetSnapshot, error) {
	if ctx == nil {
		return MutationTargetSnapshot{}, errors.New(
			"get mutation target snapshot: context is nil",
		)
	}
	if registry.snapshot == nil {
		return MutationTargetSnapshot{}, errors.New(
			"get mutation target snapshot: startup verification is incomplete",
		)
	}
	return cloneMutationTargetSnapshot(*registry.snapshot), nil
}

func (registry *MutationTargetRegistry) verifySnapshot(
	ctx context.Context,
) (MutationTargetSnapshot, error) {
	if ctx == nil {
		return MutationTargetSnapshot{}, errors.New(
			"verify mutation target cohort: context is nil",
		)
	}
	now := registry.now().UTC()
	credentials, err := registry.transport.Credentials(now)
	if err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf(
			"read WB target credentials: %w",
			err,
		)
	}
	if len(credentials) == 0 {
		return MutationTargetSnapshot{}, errors.New(
			"verify mutation target cohort: target list is empty",
		)
	}

	seenCabinets := make(map[CabinetID]struct{}, len(credentials))
	seenSellers := make(map[Digest]struct{}, len(credentials))
	for index, credential := range credentials {
		if err := validateTargetCredential(credential, now); err != nil {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"target at position %d: %w",
				index+1,
				err,
			)
		}
		if _, exists := seenCabinets[credential.CabinetID]; exists {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"cabinet %q is duplicated",
				credential.CabinetID,
			)
		}
		seenCabinets[credential.CabinetID] = struct{}{}
		if _, exists := seenSellers[credential.SellerKey]; exists {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"seller at position %d: %w",
				index+1,
				ErrDuplicateTargetSeller,
			)
		}
		seenSellers[credential.SellerKey] = struct{}{}
	}

	verified := make([]VerifiedTarget, len(credentials))
	for index, credential := range credentials {
		response, err := registry.transport.ProbeCardsList(
			ctx,
			credential.CabinetID,
			credential.ClientGeneration,
		)
		if err != nil {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"probe target %q with Cards List: %w",
				credential.CabinetID,
				err,
			)
		}
		if response.Cursor.Total < 0 ||
			len(response.Cards) > 1 {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"probe target %q returned invalid raw Cards List DTO",
				credential.CabinetID,
			)
		}
		verified[index] = VerifiedTarget(credential)
	}

	bindings, err := registry.store.SyncTargetBindings(ctx, now, verified)
	if err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf(
			"sync mutation target bindings: %w",
			err,
		)
	}
	if len(bindings) != len(verified) {
		return MutationTargetSnapshot{}, fmt.Errorf(
			"binding count %d differs from verified count %d",
			len(bindings),
			len(verified),
		)
	}

	targets := make([]MutationTarget, len(bindings))
	for index, binding := range bindings {
		if err := validateTargetBinding(binding, now); err != nil {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"binding at position %d: %w",
				index+1,
				err,
			)
		}
		if binding.CabinetID != verified[index].CabinetID ||
			binding.SellerKey != verified[index].SellerKey ||
			binding.ClientGeneration != verified[index].ClientGeneration {
			return MutationTargetSnapshot{}, fmt.Errorf(
				"binding at position %d differs from verified target",
				index+1,
			)
		}
		targets[index] = MutationTarget{
			Position:            index + 1,
			CabinetID:           binding.CabinetID,
			SellerKey:           binding.SellerKey,
			BindingRevision:     binding.BindingRevision,
			CapabilityRevision:  binding.CapabilityRevision,
			ContentRead:         binding.ContentRead,
			ContentWrite:        binding.ContentWrite,
			CredentialExpiresAt: binding.CredentialExpiresAt,
		}
	}

	snapshot := MutationTargetSnapshot{
		CohortName: registry.cohortName,
		Revision:   targetSnapshotRevision(registry.cohortName, bindings),
		Targets:    targets,
	}
	if err := snapshot.Validate(now); err != nil {
		return MutationTargetSnapshot{}, fmt.Errorf(
			"validate verified mutation target snapshot: %w",
			err,
		)
	}
	return snapshot, nil
}

func cloneMutationTargetSnapshot(
	snapshot MutationTargetSnapshot,
) MutationTargetSnapshot {
	clone := snapshot
	clone.Targets = append([]MutationTarget(nil), snapshot.Targets...)
	return clone
}

func validateTargetCredential(
	credential TargetCredential,
	now time.Time,
) error {
	switch {
	case strings.TrimSpace(string(credential.CabinetID)) !=
		string(credential.CabinetID) ||
		credential.CabinetID == "" || len(credential.CabinetID) > 128:
		return errors.New("cabinet ID is invalid")
	case credential.SellerKey == (Digest{}):
		return errors.New("seller key is empty")
	case !credential.ContentRead || !credential.ContentWrite:
		return errors.New("Content read/write capability is required")
	case credential.CredentialExpiresAt.IsZero() ||
		!credential.CredentialExpiresAt.After(now):
		return errors.New("credential is expired")
	case credential.ClientGeneration == (ClientGeneration{}):
		return errors.New("client generation is empty")
	default:
		return nil
	}
}

func validateTargetBinding(binding TargetBinding, now time.Time) error {
	if err := validateTargetCredential(TargetCredential{
		CabinetID:           binding.CabinetID,
		SellerKey:           binding.SellerKey,
		ContentRead:         binding.ContentRead,
		ContentWrite:        binding.ContentWrite,
		CredentialExpiresAt: binding.CredentialExpiresAt,
		ClientGeneration:    binding.ClientGeneration,
	}, now); err != nil {
		return err
	}
	if binding.BindingRevision <= 0 {
		return errors.New("binding revision is not positive")
	}
	if binding.CapabilityRevision <= 0 {
		return errors.New("capability revision is not positive")
	}
	return nil
}

func targetSnapshotRevision(
	cohortName string,
	bindings []TargetBinding,
) Digest {
	hasher := sha256.New()
	targetRegistryWriteString(hasher, "transfer-target-snapshot:v1")
	targetRegistryWriteString(hasher, cohortName)
	targetRegistryWriteInt64(hasher, int64(len(bindings)))
	for _, binding := range bindings {
		targetRegistryWriteString(hasher, string(binding.CabinetID))
		targetRegistryWriteBytes(hasher, binding.SellerKey[:])
		targetRegistryWriteInt64(hasher, binding.BindingRevision)
		targetRegistryWriteInt64(hasher, binding.CapabilityRevision)
		targetRegistryWriteBool(hasher, binding.ContentRead)
		targetRegistryWriteBool(hasher, binding.ContentWrite)
	}
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

func targetRegistryWriteString(writer hash.Hash, value string) {
	targetRegistryWriteBytes(writer, []byte(value))
}

func targetRegistryWriteBytes(writer hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func targetRegistryWriteInt64(writer hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}

func targetRegistryWriteBool(writer hash.Hash, value bool) {
	if value {
		targetRegistryWriteBytes(writer, []byte{1})
		return
	}
	targetRegistryWriteBytes(writer, []byte{0})
}

var _ TargetRegistry = (*MutationTargetRegistry)(nil)
