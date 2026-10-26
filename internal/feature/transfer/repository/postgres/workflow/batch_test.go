package transfer_postgres_workflow

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

type batchScanRow func(...any) error

func (row batchScanRow) Scan(dest ...any) error { return row(dest...) }

func TestScanReadyBatchHeader(t *testing.T) {
	finalizedAt := time.Now().UTC()
	row := batchScanRow(func(dest ...any) error {
		*dest[0].(*int64) = 7
		*dest[1].(*int) = 2
		*dest[2].(*int) = 1
		*dest[3].(*int64) = 13
		*dest[4].(*time.Time) = finalizedAt
		return nil
	})
	header, err := scanReadyBatchHeader(row)
	if err != nil || header.ID != 7 || header.ItemsCount != 2 || header.AuthorTelegramID != 13 {
		t.Fatalf("заголовок готовой пачки прочитан неверно: %+v %v", header, err)
	}
}

func TestScanReadyBatchItemRejectsInvalidPayload(t *testing.T) {
	card := cardpipeline.Card{
		SourceRows: []int{2}, Group: "group",
		Variant: cardpipeline.Variant{VendorCode: "sku-1"},
	}
	payload, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	row := func(encodedPayload []byte) batchScanRow {
		return func(dest ...any) error {
			*dest[0].(*int64) = 1
			*dest[1].(*int64) = 7
			*dest[2].(*int) = 1
			*dest[3].(*int) = 1
			*dest[4].(*[]byte) = encodedPayload
			return nil
		}
	}
	item, err := scanReadyBatchItem(row(payload))
	if err != nil || item.BatchID != 7 || item.SourceGroupNumber != 1 || item.Payload.Variant.VendorCode != "sku-1" {
		t.Fatalf("элемент готовой пачки прочитан неверно: %+v %v", item, err)
	}
	card.Variant.VendorCode = ""
	invalidPayload, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanReadyBatchItem(row(invalidPayload)); !errors.Is(err, core_errors.ErrConflict) {
		t.Fatalf("карточка без артикула допущена к переносу: %v", err)
	}
}
