package workflow

import (
	"testing"

	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestFairPreparationOrderInterleavesCabinets(t *testing.T) {
	works := []PreparationWork{
		{ID: 1, CabinetID: "b"},
		{ID: 2, CabinetID: "b"},
		{ID: 3, CabinetID: "b"},
		{ID: 4, CabinetID: "a"},
		{ID: 5, CabinetID: "a"},
	}

	ordered := fairPreparationOrder(works)
	want := []PreparationGroupID{4, 1, 5, 2, 3}
	if len(ordered) != len(want) {
		t.Fatalf("ordered work count = %d, want %d", len(ordered), len(want))
	}
	for index := range want {
		if ordered[index].ID != want[index] {
			t.Fatalf("work at %d = %d, want %d", index, ordered[index].ID, want[index])
		}
	}
}

func TestPreparationMayContinueWhileEarlyWavesPublish(t *testing.T) {
	for _, phase := range []transfer_service.Phase{
		transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia,
	} {
		if !preparationMayContinue(phase) {
			t.Fatalf("preparation stopped in phase %q", phase)
		}
	}
	for _, phase := range []transfer_service.Phase{
		transfer_service.PhaseInitializing,
		transfer_service.PhaseFinished,
	} {
		if preparationMayContinue(phase) {
			t.Fatalf("preparation continued in phase %q", phase)
		}
	}
}
