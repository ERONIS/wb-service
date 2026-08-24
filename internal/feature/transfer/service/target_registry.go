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

type ClientGeneration [sha256.Size]byte

type TargetCredential struct {
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
	cohortName string
	now        func() time.Time
	snapshot   *MutationTargetSnapshot
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
	credentials, err := registry.transport.Credentials()
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

func targetSnapshotRevision(
	cohortName string,
	credentials []TargetCredential,
) Digest {
	hasher := sha256.New()
	targetRegistryWriteString(hasher, "transfer-target-snapshot:v2")
	targetRegistryWriteString(hasher, cohortName)
	targetRegistryWriteInt64(hasher, int64(len(credentials)))
	for _, credential := range credentials {
		targetRegistryWriteString(hasher, string(credential.CabinetID))
		targetRegistryWriteBytes(hasher, credential.SellerKey[:])
		targetRegistryWriteBytes(hasher, credential.ClientGeneration[:])
		targetRegistryWriteInt64(hasher, credential.CredentialExpiresAt.UTC().UnixNano())
		targetRegistryWriteInt64(hasher, credential.BindingRevision)
		targetRegistryWriteInt64(hasher, credential.CapabilityRevision)
		targetRegistryWriteBool(hasher, credential.ContentRead)
		targetRegistryWriteBool(hasher, credential.ContentWrite)
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
