package transfer_postgres_repository

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

type batchScanRow func(...any) error

func (row batchScanRow) Scan(dest ...any) error { return row(dest...) }

func TestScanReadyBatchHeader(t *testing.T) {
	finalizedAt := time.Now().UTC()
	row := batchScanRow(func(dest ...any) error {
		*dest[0].(*int64) = 7
		*dest[1].(*int64) = 11
		*dest[2].(*int) = 2
		*dest[3].(*int) = 1
		*dest[4].(*int64) = 13
		*dest[5].(*time.Time) = finalizedAt
		return nil
	})
	header, err := scanReadyBatchHeader(row)
	if err != nil || header.ID != 7 || header.ItemsCount != 2 || header.AuthorTelegramID != 13 {
		t.Fatalf("заголовок готовой пачки прочитан неверно: %+v %v", header, err)
	}
}

func TestScanReadyBatchItemRejectsChangedPayload(t *testing.T) {
	fileID := cardimport_model.FileID(17)
	card := cardpipeline.Card{
		SourceRows: []int{2}, Group: "group",
		Variant: cardpipeline.Variant{VendorCode: "sku-1"},
	}
	payload, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	groupKey := cardimport_model.NewSourceGroupKey(fileID, card.Group)
	row := func(vendorCode string) batchScanRow {
		return func(dest ...any) error {
			*dest[0].(*int64) = 1
			*dest[1].(*int64) = 7
			*dest[2].(*int) = 1
			*dest[3].(*int64) = int64(fileID)
			*dest[4].(*[]int32) = []int32{2}
			*dest[5].(*[]byte) = groupKey[:]
			*dest[6].(*string) = vendorCode
			*dest[7].(*[]byte) = payload
			return nil
		}
	}
	item, err := scanReadyBatchItem(row("sku-1"))
	if err != nil || item.BatchID != 7 || item.SourceGroupKey != groupKey || item.Payload.Variant.VendorCode != "sku-1" {
		t.Fatalf("элемент готовой пачки прочитан неверно: %+v %v", item, err)
	}
	if _, err := scanReadyBatchItem(row("changed")); !errors.Is(err, core_errors.ErrConflict) {
		t.Fatalf("изменённая карточка допущена к переносу: %v", err)
	}
}
