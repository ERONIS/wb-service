package preparation_test

import (
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

func validArtifact() *preparation.PreparationArtifact {
	return &preparation.PreparationArtifact{
		SubjectID:        1,
		RequestPayload:   []byte(`[{"subjectID":1,"variants":[{"vendorCode":"ART-1"}]}]`),
		MetadataSnapshot: []byte(`{"ObservedAt":"1970-01-01T00:00:01Z","Limits":{"freeLimits":0,"paidLimits":0}}`),
		LimitsObservedAt: time.Unix(1, 0),
	}
}

func TestPreparationResultArtifactShape(t *testing.T) {
	cases := map[string]func(*preparation.PreparationArtifact){
		"payload":  func(a *preparation.PreparationArtifact) { a.RequestPayload = []byte(`{`) },
		"snapshot": func(a *preparation.PreparationArtifact) { a.MetadataSnapshot = []byte(`{`) },
		"snapshot_without_observation": func(a *preparation.PreparationArtifact) {
			a.MetadataSnapshot = []byte(`{}`)
		},
		"subject": func(a *preparation.PreparationArtifact) { a.SubjectID = 0 },
		"limits":  func(a *preparation.PreparationArtifact) { a.LimitsFree = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := validArtifact()
			now := time.Now()
			cmd := preparation.ApplyPreparationResultCommand{
				TransferID: 1, GroupTargetID: 1,
				Status: preparation.PreparationResultSucceeded, OutcomeCode: "prepared",
				StartedAt: now, FinishedAt: now, Artifact: a,
			}
			if err := cmd.Validate(); err != nil {
				t.Fatal(err)
			}
			mutate(a)
			if err := cmd.Validate(); err == nil {
				t.Fatal("некорректный артефакт принят")
			}
		})
	}
}

func TestPreparationResultRejectsEmptyRequest(t *testing.T) {
	artifact := validArtifact()
	artifact.RequestPayload = []byte(`[]`)
	now := time.Now()
	command := preparation.ApplyPreparationResultCommand{
		TransferID: 1, GroupTargetID: 1,
		Status: preparation.PreparationResultSucceeded, OutcomeCode: "prepared",
		StartedAt: now, FinishedAt: now, Artifact: artifact,
	}
	if err := command.Validate(); err == nil {
		t.Fatal("пустой запрос WB принят как успешная подготовка")
	}
}
