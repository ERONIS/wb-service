package registry

import (
	"context"
	"testing"
	"time"
)

func TestMutationSnapshotIsScopedToTelegramOwner(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	transport := ownerTargetTransportStub{byOwner: map[int64][]TargetCredential{
		101: {testTargetCredential("owner-101", 1, now.Add(time.Hour))},
		202: {testTargetCredential("owner-202", 2, now.Add(time.Hour))},
	}}
	registry := NewMutationTargetRegistry(transport, "production")
	registry.now = func() time.Time { return now }

	snapshot, err := registry.MutationSnapshot(context.Background(), 101)
	if err != nil {
		t.Fatalf("MutationSnapshot() error = %v", err)
	}
	if len(snapshot.Targets) != 1 || snapshot.Targets[0].CabinetID != "owner-101" {
		t.Fatalf("MutationSnapshot() targets = %+v", snapshot.Targets)
	}
	if _, err := registry.MutationSnapshotForOwner(
		context.Background(),
		101,
		[]CabinetID{"owner-202"},
	); err == nil {
		t.Fatal("MutationSnapshotForOwner() accepted another owner's cabinet")
	}
}

func TestFanoutMutationSnapshotUsesOnlyEligibleCredentials(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	transport := ownerTargetTransportStub{
		byOwner: map[int64][]TargetCredential{
			101: {testTargetCredential("admin-cabinet", 1, now.Add(time.Hour))},
		},
		fanout: []TargetCredential{
			testTargetCredential("partner-cabinet", 2, now.Add(time.Hour)),
			testTargetCredential("admin-cabinet", 3, now.Add(time.Hour)),
		},
	}
	registry := NewMutationTargetRegistry(transport, "production")
	registry.now = func() time.Time { return now }

	snapshot, err := registry.FanoutMutationSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FanoutMutationSnapshot() error = %v", err)
	}
	if len(snapshot.Targets) != 2 ||
		snapshot.Targets[0].CabinetID != "partner-cabinet" ||
		snapshot.Targets[1].CabinetID != "admin-cabinet" {
		t.Fatalf("FanoutMutationSnapshot() targets = %+v", snapshot.Targets)
	}
}

func testTargetCredential(
	cabinetID CabinetID,
	seed byte,
	expiresAt time.Time,
) TargetCredential {
	credential := TargetCredential{
		CabinetID:           cabinetID,
		BindingRevision:     1,
		CapabilityRevision:  1,
		ContentRead:         true,
		ContentWrite:        true,
		CredentialExpiresAt: expiresAt,
	}
	credential.SellerKey[0] = seed
	credential.ClientGeneration[0] = seed
	return credential
}

type ownerTargetTransportStub struct {
	byOwner map[int64][]TargetCredential
	fanout  []TargetCredential
}

func (transport ownerTargetTransportStub) FanoutCredentials(
	context.Context,
) ([]TargetCredential, error) {
	return append([]TargetCredential(nil), transport.fanout...), nil
}

func (transport ownerTargetTransportStub) Credentials(
	_ context.Context,
	ownerTelegramID int64,
) ([]TargetCredential, error) {
	return append([]TargetCredential(nil), transport.byOwner[ownerTelegramID]...), nil
}

func (transport ownerTargetTransportStub) AllCredentials(context.Context) ([]TargetCredential, error) {
	var result []TargetCredential
	for _, credentials := range transport.byOwner {
		result = append(result, credentials...)
	}
	return result, nil
}

var _ TargetTransport = ownerTargetTransportStub{}
