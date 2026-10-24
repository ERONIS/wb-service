package preparation

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
)

func TestInvalidDirectoryValueIncludesBoundedDiagnostics(t *testing.T) {
	directory := make([]string, 12)
	for index := range directory {
		directory[index] = fmt.Sprintf("Цвет %d", index)
	}
	input := strings.Repeat("Ж", 170)
	_, result := characteristicValue(
		7,
		contentapi.SubjectCharacteristic{
			CharacteristicID:   42,
			Name:               "Цвет",
			CharacteristicType: 1,
		},
		input,
		cardprepare_model.DirectorySnapshot{Colors: directory},
	)
	if result == nil || result.Outcome.Code != OutcomeDirectoryValueInvalid {
		t.Fatalf("expected directory rejection, got %+v", result)
	}
	details := result.Outcome.Details
	if details == nil || details.CharacteristicID != 42 || details.CharacteristicName != "Цвет" {
		t.Fatalf("missing characteristic identity: %+v", details)
	}
	if details.DirectoryValueCount != len(directory) || len(details.DirectoryValuesSample) != 8 {
		t.Fatalf("unexpected directory summary: %+v", details)
	}
	value, ok := details.CharacteristicValue.(string)
	if !ok || utf8.RuneCountInString(value) != 161 || !strings.HasSuffix(value, "…") {
		t.Fatalf("invalid bounded source value: %#v", details.CharacteristicValue)
	}
}
