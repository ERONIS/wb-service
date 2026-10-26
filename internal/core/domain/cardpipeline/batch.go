package cardpipeline

import (
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

type (
	BatchID     int64
	BatchItemID int64
)

type BatchCursor struct {
	FinalizedAt time.Time
	BatchID     BatchID
}

type ReadyBatch struct {
	Header ReadyBatchHeader
	Items  []ReadyBatchItem
}

type ReadyBatchHeader struct {
	ID               BatchID
	ItemsCount       int
	GroupsCount      int
	AuthorTelegramID int64
	FinalizedAt      time.Time
}

type ReadyBatchItem struct {
	ID                BatchItemID
	BatchID           BatchID
	Position          int
	SourceGroupNumber int
	Payload           Card
}

func (cursor BatchCursor) Validate() error {
	if cursor.FinalizedAt.IsZero() || cursor.BatchID <= 0 {
		return core_errors.InvalidArgument("invalid finalized batch cursor")
	}
	return nil
}

func (header ReadyBatchHeader) Validate() error {
	if header.ID <= 0 ||
		header.ItemsCount <= 0 || header.GroupsCount <= 0 ||
		header.GroupsCount > header.ItemsCount ||
		header.AuthorTelegramID <= 0 ||
		header.FinalizedAt.IsZero() {
		return invalidBatch("ready batch header is invalid")
	}
	return nil
}

func (item ReadyBatchItem) Validate() error {
	if item.ID <= 0 || item.BatchID <= 0 || item.Position <= 0 ||
		item.SourceGroupNumber <= 0 ||
		strings.TrimSpace(item.Payload.Group) == "" ||
		strings.TrimSpace(item.Payload.Variant.VendorCode) == "" ||
		len(item.Payload.SourceRows) == 0 {
		return invalidBatch("ready batch item is invalid")
	}
	for index, row := range item.Payload.SourceRows {
		if row <= 0 || index > 0 && row <= item.Payload.SourceRows[index-1] {
			return invalidBatch("ready batch source rows are invalid")
		}
	}
	return nil
}

func invalidBatch(message string) error {
	return core_errors.InvalidArgument("invalid card pipeline batch: %s", message)
}
