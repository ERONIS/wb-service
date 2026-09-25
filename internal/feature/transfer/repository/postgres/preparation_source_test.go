package transfer_postgres_repository

import (
	"testing"

	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestValidPreparationActivationAcceptsPartiallyCompletedRetry(t *testing.T) {
	t.Parallel()

	// The transfer has 510 running and 16 terminal item targets. Both states
	// are valid when resuming preparation, so all 526 items are active.
	if !validPreparationActivation(468, 526, 468, 468, 526, 526) {
		t.Fatal("partially completed preparation retry was rejected")
	}
}

func TestPipelinePhasesAllowOverlappingPreparationAndPublication(t *testing.T) {
	for _, phase := range []transfer_service.Phase{
		transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia,
	} {
		if !preparationPipelinePhase(string(phase)) || !publicationPlanningPhase(phase) {
			t.Fatalf("pipeline work stopped in phase %q", phase)
		}
	}
	if preparationPipelinePhase(string(transfer_service.PhaseFinished)) ||
		publicationPlanningPhase(transfer_service.PhaseFinished) {
		t.Fatal("pipeline work continued after transfer finished")
	}
}

func TestValidPreparationActivationRejectsIncompleteProjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                                               string
		totalGroups, activeGroups, totalItems, activeItems int64
	}{
		{name: "missing group", totalGroups: 467, activeGroups: 467, totalItems: 526, activeItems: 526},
		{name: "inactive group", totalGroups: 468, activeGroups: 467, totalItems: 526, activeItems: 526},
		{name: "missing item", totalGroups: 468, activeGroups: 468, totalItems: 525, activeItems: 525},
		{name: "pending item", totalGroups: 468, activeGroups: 468, totalItems: 526, activeItems: 525},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if validPreparationActivation(
				468,
				526,
				test.totalGroups,
				test.activeGroups,
				test.totalItems,
				test.activeItems,
			) {
				t.Fatal("incomplete preparation projection was accepted")
			}
		})
	}
}
