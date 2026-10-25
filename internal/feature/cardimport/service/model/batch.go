package cardimport_model

import (
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const MaxBatchPageSize = 1000

type BatchHeader struct {
	ID               cardpipeline.BatchID
	SourceSessionID  SessionID
	ItemsCount       int
	GroupsCount      int
	AuthorTelegramID int64
	FinalizedAt      time.Time
}

type BatchItem struct {
	ID             cardpipeline.BatchItemID
	BatchID        cardpipeline.BatchID
	Position       int
	SourceFileID   FileID
	SourceRows     []int
	SourceGroupKey cardpipeline.SourceGroupKey
	VendorCode     string
	Payload        cardpipeline.Card
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
	case len(item.SourceRows) == 0:
		return invalidBatch("item source rows are empty")
	case strings.TrimSpace(item.VendorCode) == "":
		return invalidBatch("item vendor code is empty")
	case item.Payload.Variant.VendorCode != item.VendorCode:
		return invalidBatch("item vendor code differs from payload")
	case !equalRows(item.SourceRows, item.Payload.SourceRows):
		return invalidBatch("item source rows differ from payload")
	case strings.TrimSpace(item.Payload.Group) == "":
		return invalidBatch("item group is empty")
	case item.SourceGroupKey != NewSourceGroupKey(item.SourceFileID, item.Payload.Group):
		return invalidBatch("item source group key differs from payload")
	}
	for index, row := range item.SourceRows {
		if row <= 0 {
			return invalidBatch("item source row must be positive")
		}
		if index > 0 && row <= item.SourceRows[index-1] {
			return invalidBatch("item source rows must be strictly increasing")
		}
	}
	return nil
}

// NewSourceGroupKey создаёт ключ группы из ID файла и значения группы.
func NewSourceGroupKey(fileID FileID, group string) cardpipeline.SourceGroupKey {
	builder := canonicalhash.NewSHA256("cardimport-source-group:v1")
	builder.Int64(int64(fileID))
	builder.String("group")
	builder.String(group)
	return cardpipeline.SourceGroupKey(builder.Sum())
}

// equalRows сравнивает номера исходных строк по порядку.
func equalRows(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// invalidBatch оформляет ошибку некорректной пачки.
func invalidBatch(message string) error {
	return core_errors.InvalidArgument("invalid card pipeline batch: %s", message)
}
