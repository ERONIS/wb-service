package v1

import (
	"strings"
	"testing"

	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

func TestContentCatalogOperationsAreCompleteAndValid(t *testing.T) {
	t.Parallel()

	operations := []policy.Operation{
		ParentCategoriesOperation(),
		SubjectsOperation(),
		mustSubjectCharacteristicsOperation(t, 1),
		CardsLimitsOperation(),
		BrandsOperation(),
		DirectoryColorsOperation(),
		DirectoryKindsOperation(),
		DirectoryCountriesOperation(),
		DirectorySeasonsOperation(),
		DirectoryVATOperation(),
		DirectoryTNVEDOperation(),
		CardsListOperation(),
		TrashCardsListOperation(),
		CardsErrorListOperation(),
		UploadCardsOperation(),
		UploadCardsAddOperation(),
		SaveMediaByLinksOperation(),
	}

	seen := make(map[policy.OperationID]struct{}, len(operations))
	for _, operation := range operations {
		if operation.ID() == "" {
			t.Fatal("catalog contains empty operation ID")
		}
		if _, exists := seen[operation.ID()]; exists {
			t.Fatalf("duplicate operation ID %q", operation.ID())
		}
		seen[operation.ID()] = struct{}{}

		if !strings.HasPrefix(operation.Path(), "/") {
			t.Fatalf("operation %q path = %q", operation.ID(), operation.Path())
		}
		if operation.BucketID() == "" {
			t.Fatalf("operation %q has empty bucket", operation.ID())
		}
		if operation.MaxResponseBytes() <= 0 {
			t.Fatalf("operation %q has invalid response bound", operation.ID())
		}
		if operation.Kind().IsMutation() && operation.RetryMode().AllowsRetry() {
			t.Fatalf("mutation %q allows retry", operation.ID())
		}
	}
}

func TestSubjectCharacteristicsOperationValidatesAndEscapesID(t *testing.T) {
	t.Parallel()

	operation, err := SubjectCharacteristicsOperation(123)
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	if got, want := operation.Path(), "/content/v2/object/charcs/123"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	if _, err := SubjectCharacteristicsOperation(0); err == nil {
		t.Fatal("zero subject ID unexpectedly accepted")
	}
	if _, err := SubjectCharacteristicsOperation(-1); err == nil {
		t.Fatal("negative subject ID unexpectedly accepted")
	}
}

func TestBucketSpecsAreUniqueCopies(t *testing.T) {
	t.Parallel()

	first := BucketSpecs()
	second := BucketSpecs()
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("bucket spec lengths = %d and %d", len(first), len(second))
	}

	seen := make(map[policy.BucketID]struct{}, len(first))
	for _, spec := range first {
		if err := spec.Validate(); err != nil {
			t.Fatalf("invalid bucket %q: %v", spec.ID, err)
		}
		if _, exists := seen[spec.ID]; exists {
			t.Fatalf("duplicate bucket ID %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
	}

	first[0].ID = "mutated"
	if second[0].ID == "mutated" {
		t.Fatal("BucketSpecs returned shared storage")
	}
}

func mustSubjectCharacteristicsOperation(
	t *testing.T,
	subjectID int64,
) policy.Operation {
	t.Helper()

	operation, err := SubjectCharacteristicsOperation(subjectID)
	if err != nil {
		t.Fatalf("create subject characteristics operation: %v", err)
	}

	return operation
}
