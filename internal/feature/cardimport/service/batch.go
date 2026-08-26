package cardimport_service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const (
	BatchSchemaVersion        = 1
	BatchNormalizationVersion = 1
	MaxBatchPageSize          = 1000
)

type BatchID int64
type BatchItemID int64
type Digest [sha256.Size]byte
type SourceGroupKey [sha256.Size]byte

type BatchCursor struct {
	FinalizedAt time.Time
	BatchID     BatchID
}

func (cursor BatchCursor) Validate() error {
	if cursor.FinalizedAt.IsZero() || cursor.BatchID <= 0 {
		return fmt.Errorf(
			"invalid finalized batch cursor: %w",
			core_errors.ErrInvalidArgument,
		)
	}
	return nil
}

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

func (key SourceGroupKey) String() string {
	return hex.EncodeToString(key[:])
}

type TrustedActor struct {
	TelegramUserID int64
	DisplayName    string
}

func (a TrustedActor) normalized() TrustedActor {
	a.DisplayName = strings.TrimSpace(a.DisplayName)
	return a
}

func (a TrustedActor) validate() error {
	switch {
	case a.TelegramUserID <= 0:
		return fmt.Errorf(
			"invalid trusted actor Telegram ID: %w",
			core_errors.ErrInvalidArgument,
		)
	case a.DisplayName == "":
		return fmt.Errorf(
			"trusted actor display name is empty: %w",
			core_errors.ErrInvalidArgument,
		)
	case len([]rune(a.DisplayName)) > 100:
		return fmt.Errorf(
			"trusted actor display name is too long: %w",
			core_errors.ErrInvalidArgument,
		)
	default:
		return nil
	}
}

type AuthorSnapshot struct {
	UserID         *int64 `json:"userId,omitempty"`
	TelegramUserID int64  `json:"telegramUserId"`
	DisplayName    string `json:"displayName"`
}

type BatchHeader struct {
	ID                   BatchID
	SourceSessionID      SessionID
	Purpose              Purpose
	ItemsCount           int
	GroupsCount          int
	SchemaVersion        int
	NormalizationVersion int
	Checksum             Digest
	AuthorSnapshot       AuthorSnapshot
	FinalizedAt          time.Time
}

func (h BatchHeader) Validate() error {
	switch {
	case h.ID <= 0:
		return invalidBatch("batch ID must be positive")
	case h.SourceSessionID <= 0:
		return invalidBatch("batch source session ID must be positive")
	case !h.Purpose.IsValid():
		return invalidBatch("purpose is invalid")
	case h.ItemsCount <= 0:
		return invalidBatch("items count must be positive")
	case h.GroupsCount <= 0 || h.GroupsCount > h.ItemsCount:
		return invalidBatch("groups count is outside allowed bounds")
	case h.SchemaVersion <= 0:
		return invalidBatch("schema version must be positive")
	case h.NormalizationVersion <= 0:
		return invalidBatch("normalization version must be positive")
	case h.AuthorSnapshot.TelegramUserID <= 0:
		return invalidBatch("author Telegram ID must be positive")
	case h.AuthorSnapshot.UserID != nil && *h.AuthorSnapshot.UserID <= 0:
		return invalidBatch("author user ID must be positive")
	case strings.TrimSpace(h.AuthorSnapshot.DisplayName) == "":
		return invalidBatch("author display name is empty")
	case len([]rune(h.AuthorSnapshot.DisplayName)) > 100:
		return invalidBatch("author display name is too long")
	case h.FinalizedAt.IsZero():
		return invalidBatch("finalized time is empty")
	default:
		return nil
	}
}

type BatchItem struct {
	ID             BatchItemID
	BatchID        BatchID
	Position       int
	SourceFileID   FileID
	SourceRows     []int
	SourceGroupKey SourceGroupKey
	VendorCode     string
	Payload        AggregatedCard
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
	case item.SourceGroupKey != NewSourceGroupKey(
		item.SourceFileID,
		item.Payload.Group,
		item.VendorCode,
	):
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

func NewSourceGroupKey(
	fileID FileID,
	group string,
	vendorCode string,
) SourceGroupKey {
	hasher := sha256.New()
	canonicalWriteString(hasher, "cardimport-source-group:v1")
	canonicalWriteInt64(hasher, int64(fileID))
	if group != "" {
		canonicalWriteString(hasher, "group")
		canonicalWriteString(hasher, group)
	} else {
		canonicalWriteString(hasher, "singleton")
		canonicalWriteString(hasher, vendorCode)
	}

	var result SourceGroupKey
	copy(result[:], hasher.Sum(nil))
	return result
}

func BatchChecksum(
	purpose Purpose,
	groupsCount int,
	items []BatchItem,
) (Digest, error) {
	hasher := sha256.New()
	canonicalWriteString(hasher, "cardimport-batch-semantic:v1")
	canonicalWriteString(hasher, string(purpose))
	canonicalWriteInt64(hasher, BatchSchemaVersion)
	canonicalWriteInt64(hasher, BatchNormalizationVersion)
	canonicalWriteInt64(hasher, int64(len(items)))
	canonicalWriteInt64(hasher, int64(groupsCount))

	for index, item := range items {
		if item.Position != index+1 {
			return Digest{}, invalidBatch(
				"item positions must be contiguous and ordered",
			)
		}
		if err := item.Validate(); err != nil {
			return Digest{}, err
		}

		payload, err := json.Marshal(item.Payload)
		if err != nil {
			return Digest{}, fmt.Errorf(
				"marshal batch item payload at position %d: %w",
				item.Position,
				err,
			)
		}

		canonicalWriteInt64(hasher, int64(item.Position))
		canonicalWriteInt64(hasher, int64(item.SourceFileID))
		canonicalWriteInt64(hasher, int64(len(item.SourceRows)))
		for _, row := range item.SourceRows {
			canonicalWriteInt64(hasher, int64(row))
		}
		canonicalWriteBytes(hasher, item.SourceGroupKey[:])
		canonicalWriteString(hasher, item.VendorCode)
		canonicalWriteBytes(hasher, payload)
	}

	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func canonicalWriteString(writer hash.Hash, value string) {
	canonicalWriteBytes(writer, []byte(value))
}

func canonicalWriteBytes(writer hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func canonicalWriteInt64(writer hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}

func invalidBatch(message string) error {
	return fmt.Errorf(
		"invalid cardimport batch: %s: %w",
		message,
		core_errors.ErrInvalidArgument,
	)
}
