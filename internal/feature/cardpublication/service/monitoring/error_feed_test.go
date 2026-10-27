package monitoring

import (
	"reflect"
	"testing"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

func TestNormalizeErrorBatchPreservesDistinctErrorsWithoutHashes(t *testing.T) {
	batch, err := normalizeErrorBatch(core_domain.SellerID("00000000-0000-0000-0000-000000000001"), contentapi.CardsErrorBatch{
		BatchUUID: "batch-1",
		UpdatedAt: "2026-01-02T03:04:05Z",
		Errors: map[string][]string{
			"A": {"ошибка размера", "ошибка цвета", "ошибка размера"},
			"B": {"ошибка цвета"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"wb_error_1", "wb_error_2"}; !reflect.DeepEqual(batch.ErrorCodes, want) {
		t.Fatalf("коды ошибок: получено %v, ожидалось %v", batch.ErrorCodes, want)
	}
	if want := []string{"A", "B"}; !reflect.DeepEqual(batch.RejectedVendorCodes, want) {
		t.Fatalf("отклонённые артикулы: получено %v, ожидалось %v", batch.RejectedVendorCodes, want)
	}
}
