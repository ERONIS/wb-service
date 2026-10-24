package cardpipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

const (
	MaxBatchPageSize         = 1000
	PurposeTransfer  Purpose = "transfer"
)

type (
	BatchID        int64
	BatchItemID    int64
	SourceGroupKey [sha256.Size]byte
	Digest         [sha256.Size]byte
	Purpose        string
)

type BatchCursor struct {
	FinalizedAt time.Time
	BatchID     BatchID
}

func (cursor BatchCursor) Validate() error {
	if cursor.FinalizedAt.IsZero() || cursor.BatchID <= 0 {
		return core_errors.InvalidArgument("invalid finalized batch cursor")
	}
	return nil
}

func (key SourceGroupKey) String() string {
	return hex.EncodeToString(key[:])
}

func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}

func (purpose Purpose) IsValid() bool {
	return purpose == PurposeTransfer
}

func invalidBatch(message string) error {
	return core_errors.InvalidArgument("invalid card pipeline batch: %s", message)
}
