package cardimport_model

import (
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

type BatchHeader struct {
	ID               cardpipeline.BatchID
	SourceSessionID  SessionID
	ItemsCount       int
	GroupsCount      int
	AuthorTelegramID int64
	FinalizedAt      time.Time
}

type BatchItem struct {
	ID                cardpipeline.BatchItemID
	BatchID           cardpipeline.BatchID
	Position          int
	SourceFileID      FileID
	SourceGroupNumber int
	Payload           cardpipeline.Card
}

// Validate проверяет состав и автора готовой пачки.
func (header BatchHeader) Validate() error {
	switch {
	case header.ID <= 0:
		return invalidBatch("batch ID must be positive")
	case header.SourceSessionID <= 0:
		return invalidBatch("batch source session ID must be positive")
	case header.ItemsCount <= 0:
		return invalidBatch("items count must be positive")
	case header.GroupsCount <= 0 || header.GroupsCount > header.ItemsCount:
		return invalidBatch("groups count is outside allowed bounds")
	case header.AuthorTelegramID <= 0:
		return invalidBatch("author Telegram ID must be positive")
	case header.FinalizedAt.IsZero():
		return invalidBatch("finalized time is empty")
	default:
		return nil
	}
}

// Validate проверяет связь карточки с исходным файлом и группой.
func (item BatchItem) Validate() error {
	switch {
	case item.Position <= 0:
		return invalidBatch("item position must be positive")
	case item.SourceFileID <= 0:
		return invalidBatch("item source file ID must be positive")
	case len(item.Payload.SourceRows) == 0:
		return invalidBatch("item source rows are empty")
	case strings.TrimSpace(item.Payload.Variant.VendorCode) == "":
		return invalidBatch("item vendor code is empty")
	case strings.TrimSpace(item.Payload.Group) == "":
		return invalidBatch("item group is empty")
	case item.SourceGroupNumber <= 0:
		return invalidBatch("item source group number must be positive")
	}
	for index, row := range item.Payload.SourceRows {
		if row <= 0 {
			return invalidBatch("item source row must be positive")
		}
		if index > 0 && row <= item.Payload.SourceRows[index-1] {
			return invalidBatch("item source rows must be strictly increasing")
		}
	}
	return nil
}

// invalidBatch оформляет ошибку некорректной пачки.
func invalidBatch(message string) error {
	return core_errors.InvalidArgument("invalid card pipeline batch: %s", message)
}
