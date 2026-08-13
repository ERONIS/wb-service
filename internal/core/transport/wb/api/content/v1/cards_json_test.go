package v1

import (
	"encoding/json"
	"testing"
)

func TestUploadCardsRequestOmitsEmptySKUs(t *testing.T) {
	t.Parallel()

	request := UploadCardsRequest{{
		SubjectID: 42,
		Variants: []UploadCard{{
			VendorCode: "vendor-1",
			Sizes:      []UploadSize{{}},
		}},
	}}

	assertJSONEqual(
		t,
		request,
		`[{"subjectID":42,"variants":[{"vendorCode":"vendor-1","dimensions":{"length":0,"width":0,"height":0,"weightBrutto":0},"sizes":[{}]}]}]`,
	)
}

func TestUploadCardsAddRequestOmitsEmptySKUs(t *testing.T) {
	t.Parallel()

	request := UploadCardsAddRequest{
		IMTID: 77,
		CardsToAdd: []UploadCard{{
			VendorCode: "vendor-1",
			Sizes:      []UploadSize{{}},
		}},
	}

	assertJSONEqual(
		t,
		request,
		`{"imtID":77,"cardsToAdd":[{"vendorCode":"vendor-1","dimensions":{"length":0,"width":0,"height":0,"weightBrutto":0},"sizes":[{}]}]}`,
	)
}

func TestUploadSizePreservesSuppliedSKUs(t *testing.T) {
	t.Parallel()

	request := UploadSize{SKUs: []string{"4601234567890"}}

	assertJSONEqual(t, request, `{"skus":["4601234567890"]}`)
}

func assertJSONEqual(t *testing.T, value any, expected string) {
	t.Helper()

	actual, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(actual) != expected {
		t.Fatalf("JSON = %s, want %s", actual, expected)
	}
}
