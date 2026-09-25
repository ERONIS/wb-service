package registry

import (
	"context"
	"testing"
	"time"
)

func TestMutationSnapshotForSelectsAndRepositionsExactCabinet(t *testing.T) {
	now := time.Now().UTC()
	registry := &MutationTargetRegistry{now: func() time.Time { return now }}
	registry.snapshot = &MutationTargetSnapshot{
		CohortName: "production",
		Revision:   Digest{1},
		Targets: []MutationTarget{
			testMutationTarget("first", 1, now),
			testMutationTarget("second", 2, now),
		},
	}

	snapshot, err := registry.MutationSnapshotFor(context.Background(), []CabinetID{"second"})
	if err != nil {
		t.Fatalf("MutationSnapshotFor() error = %v", err)
	}
	if len(snapshot.Targets) != 1 || snapshot.Targets[0].CabinetID != "second" || snapshot.Targets[0].Position != 1 {
		t.Fatalf("selected targets = %#v", snapshot.Targets)
	}
}

func testMutationTarget(id CabinetID, marker byte, now time.Time) MutationTarget {
	return MutationTarget{
		Position:            int(marker),
		CabinetID:           id,
		SellerKey:           SellerKey{marker},
		ClientGeneration:    ClientGeneration{marker},
		BindingRevision:     1,
		CapabilityRevision:  1,
		ContentRead:         true,
		ContentWrite:        true,
		CredentialExpiresAt: now.Add(time.Hour),
	}
}
