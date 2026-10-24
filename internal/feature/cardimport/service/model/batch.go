package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/canonicalhash"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const (
	MaxBatchPageSize = cardpipeline.MaxBatchPageSize
)

type BatchID = cardpipeline.BatchID
type BatchItemID = cardpipeline.BatchItemID
type Digest = cardpipeline.Digest
type SourceGroupKey = cardpipeline.SourceGroupKey
type BatchCursor = cardpipeline.BatchCursor

type AuthorSnapshot struct {
	UserID         *int64 `json:"userId,omitempty"`
	TelegramUserID int64  `json:"telegramUserId"`
	DisplayName    string `json:"displayName"`
}

type BatchHeader struct {
	ID              BatchID
	SourceSessionID SessionID
	Purpose         Purpose
	ItemsCount      int
	GroupsCount     int
	Checksum        Digest
	AuthorSnapshot  AuthorSnapshot
	FinalizedAt     time.Time
}

type BatchItem struct {
	ID             BatchItemID
	BatchID        BatchID
	Position       int
	SourceFileID   FileID
	SourceRows     []int
	SourceGroupKey SourceGroupKey
	VendorCode     string
	Payload        cardpipeline.Card
}

func BatchChecksum(purpose Purpose, groupsCount int, items []BatchItem) (Digest, error) {
	builder := canonicalhash.NewSHA256("cardimport-batch-semantic")
	builder.String(string(purpose))
	builder.Int64(int64(len(items)))
	builder.Int64(int64(groupsCount))

	for index, item := range items {
		if item.Position != index+1 {
			return Digest{}, invalidBatch("item positions must be contiguous and ordered")
		}
		if err := item.Validate(); err != nil {
			return Digest{}, err
		}
		payload, err := json.Marshal(item.Payload)
		if err != nil {
			return Digest{}, fmt.Errorf("marshal batch item payload at position %d: %w", item.Position, err)
		}
		builder.Int64(int64(item.Position))
		builder.Int64(int64(item.SourceFileID))
		builder.Int64(int64(len(item.SourceRows)))
		for _, row := range item.SourceRows {
			builder.Int64(int64(row))
		}
		builder.Bytes(item.SourceGroupKey[:])
		builder.String(item.VendorCode)
		builder.Bytes(payload)
	}
	return Digest(builder.Sum()), nil
}

func NewSourceGroupKey(fileID FileID, group string, vendorCode string) SourceGroupKey {
	builder := canonicalhash.NewSHA256("cardimport-source-group:v1")
	builder.Int64(int64(fileID))
	if group != "" {
		builder.String("group")
		builder.String(group)
	} else {
		builder.String("singleton")
		builder.String(vendorCode)
	}
	return SourceGroupKey(builder.Sum())
}

func (header BatchHeader) Validate() error {
	switch {
	case header.ID <= 0:
		return invalidBatch("batch ID must be positive")
	case header.SourceSessionID <= 0:
		return invalidBatch("batch source session ID must be positive")
	case !header.Purpose.IsValid():
		return invalidBatch("purpose is invalid")
	case header.ItemsCount <= 0:
		return invalidBatch("items count must be positive")
	case header.GroupsCount <= 0 || header.GroupsCount > header.ItemsCount:
		return invalidBatch("groups count is outside allowed bounds")
	case header.AuthorSnapshot.TelegramUserID <= 0:
		return invalidBatch("author Telegram ID must be positive")
	case header.AuthorSnapshot.UserID != nil && *header.AuthorSnapshot.UserID <= 0:
		return invalidBatch("author user ID must be positive")
	case strings.TrimSpace(header.AuthorSnapshot.DisplayName) == "":
		return invalidBatch("author display name is empty")
	case len([]rune(header.AuthorSnapshot.DisplayName)) > 100:
		return invalidBatch("author display name is too long")
	case header.FinalizedAt.IsZero():
		return invalidBatch("finalized time is empty")
	default:
		return nil
	}
}

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
	case item.SourceGroupKey != NewSourceGroupKey(item.SourceFileID, item.Payload.Group, item.VendorCode):
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

func invalidBatch(message string) error {
	return core_errors.InvalidArgument("invalid card pipeline batch: %s", message)
}
