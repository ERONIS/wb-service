package preparation

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

func TestPreparePreservesTypedCharacteristicsAndPerSizePrices(t *testing.T) {
	group := SourceGroup{Items: []SourceItem{{
		Position: 1,
		Card: cardimport_service.AggregatedCard{
			SourceRows: []int{1},
			Category:   "Футболки",
			Price:      999,
			Variant: cardimport_service.ParsedVariant{
				VendorCode: "vendor-1",
				Title:      "Title",
				Dimensions: cardimport_service.ParsedDimensions{Length: 1, Width: 2, Height: 3, WeightBrutto: 0.5},
				Sizes: []cardimport_service.ParsedSize{
					{TechSize: "S", Price: 1100, SKUs: []string{}},
					{TechSize: "M", Price: 1200, SKUs: []string{}},
				},
				Characteristics: []cardimport_service.RawCharacteristic{{
					ID: 10, Name: "Старое имя", Value: []any{"хлопок"},
				}},
			},
		},
	}}}
	now := time.Now().UTC()
	snapshot := CatalogSnapshot{
		Subjects: []contentapi.Subject{{SubjectID: 1, SubjectName: "Футболки"}},
		Characteristics: []contentapi.SubjectCharacteristic{{
			CharacteristicID:   10,
			SubjectID:          1,
			Name:               "Материал",
			Required:           true,
			MaxCount:           1,
			CharacteristicType: 1,
		}},
		Limits:     contentapi.CardsLimits{FreeLimits: 10},
		ObservedAt: now,
	}

	result, err := Prepare(group, snapshot)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Outcome.Code != OutcomePrepared || result.Proposal == nil {
		t.Fatalf("Prepare() result = %#v", result)
	}
	variant := result.Proposal.Request[0].Variants[0]
	if got := []int64{*variant.Sizes[0].Price, *variant.Sizes[1].Price}; !reflect.DeepEqual(got, []int64{1200, 1100}) && !reflect.DeepEqual(got, []int64{1100, 1200}) {
		t.Fatalf("size prices = %v", got)
	}
	if !reflect.DeepEqual(variant.Characteristics[0].Value, []string{"хлопок"}) {
		t.Fatalf("characteristic value = %#v", variant.Characteristics[0].Value)
	}
	if !bytes.Contains(result.Proposal.EncodedRequest, []byte(`"skus":[]`)) {
		t.Fatalf("encoded request does not contain empty SKUs: %s", result.Proposal.EncodedRequest)
	}
}

func TestPrepareSkipsDeprecatedCharacteristic(t *testing.T) {
	group := SourceGroup{Items: []SourceItem{{
		Position: 1,
		Card: cardimport_service.AggregatedCard{
			SourceRows: []int{1},
			Category:   "Акустические системы",
			Price:      999,
			Variant: cardimport_service.ParsedVariant{
				VendorCode: "vendor-1",
				Title:      "Title",
				Dimensions: cardimport_service.ParsedDimensions{
					Length: 1, Width: 2, Height: 3, WeightBrutto: 0.5,
				},
				Sizes: []cardimport_service.ParsedSize{{
					Price: 999, SKUs: []string{},
				}},
				Characteristics: []cardimport_service.RawCharacteristic{{
					ID: 10, Name: "Устаревшая характеристика", Value: "старое значение",
				}},
			},
		},
	}}}
	snapshot := CatalogSnapshot{
		Subjects: []contentapi.Subject{{
			SubjectID: 1, SubjectName: "Акустические системы",
		}},
		Characteristics: []contentapi.SubjectCharacteristic{{
			CharacteristicID:   10,
			SubjectID:          1,
			Name:               "Устаревшая характеристика",
			Required:           true,
			MaxCount:           1,
			CharacteristicType: 0,
		}},
		Limits:     contentapi.CardsLimits{FreeLimits: 10},
		ObservedAt: time.Now().UTC(),
	}

	result, err := Prepare(group, snapshot)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Outcome.Code != OutcomePrepared || result.Proposal == nil {
		t.Fatalf("Prepare() result = %#v", result)
	}
	variant := result.Proposal.Request[0].Variants[0]
	if len(variant.Characteristics) != 0 {
		t.Fatalf("deprecated characteristics were included: %#v", variant.Characteristics)
	}
	if bytes.Contains(result.Proposal.EncodedRequest, []byte(`"id":10`)) {
		t.Fatalf("encoded request contains deprecated characteristic: %s", result.Proposal.EncodedRequest)
	}
}

func TestUsedDirectoryKindsPrefersCharacteristicID(t *testing.T) {
	group := SourceGroup{Items: []SourceItem{{
		Position: 1,
		Card: cardimport_service.AggregatedCard{Variant: cardimport_service.ParsedVariant{
			Characteristics: []cardimport_service.RawCharacteristic{{
				ID: 77, Name: "Устаревшее имя", Value: []any{"красный"},
			}},
		}},
	}}}
	characteristics := []contentapi.SubjectCharacteristic{{
		CharacteristicID: 77,
		Name:             "Цвет",
	}}

	used := usedDirectoryKinds(group, characteristics)
	if !used[directoryColors] {
		t.Fatalf("used directories = %#v", used)
	}
}

func TestUsedDirectoryKindsRecognizesTNVEDAliasesAndPrepareSucceeds(t *testing.T) {
	group := SourceGroup{Items: []SourceItem{{
		Position: 1,
		Card: cardimport_service.AggregatedCard{
			SourceRows: []int{1},
			Category:   "Футболки",
			Price:      999,
			Variant: cardimport_service.ParsedVariant{
				VendorCode: "vendor-1",
				Title:      "Title",
				Dimensions: cardimport_service.ParsedDimensions{Length: 1, Width: 2, Height: 3, WeightBrutto: 0.5},
				Sizes: []cardimport_service.ParsedSize{
					{TechSize: "S", Price: 1100, SKUs: []string{}},
				},
				Characteristics: []cardimport_service.RawCharacteristic{{
					ID: 0, Name: "Код ТН ВЭД", Value: []any{"8201900009"},
				}},
			},
		},
	}}}
	characteristics := []contentapi.SubjectCharacteristic{{
		CharacteristicID:   15000001,
		SubjectID:          1,
		Name:               "ТНВЭД",
		Required:           false,
		MaxCount:           1,
		CharacteristicType: 1,
	}}

	used := usedDirectoryKinds(group, characteristics)
	if !used[directoryTNVED] {
		t.Fatalf("used directories should contain directoryTNVED, got %#v", used)
	}

	// Also verify Prepare does not reject with directory_not_loaded even if directories.TNVED is nil
	snapshot := CatalogSnapshot{
		Subjects:        []contentapi.Subject{{SubjectID: 1, SubjectName: "Футболки"}},
		Characteristics: characteristics,
		Limits:          contentapi.CardsLimits{FreeLimits: 10},
		ObservedAt:      time.Now().UTC(),
		Directories:     DirectorySnapshot{TNVED: nil},
	}
	result, err := Prepare(group, snapshot)
	if err != nil {
		t.Fatalf("Prepare() unexpected error = %v", err)
	}
	if result.Outcome.Code != OutcomePrepared {
		t.Fatalf("Prepare() expected OutcomePrepared, got %s (field: %s)", result.Outcome.Code, result.Outcome.Field)
	}
}
