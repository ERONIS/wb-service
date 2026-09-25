package cardpipeline

import (
	"crypto/sha256"
	"encoding/hex"
)

type BatchID int64

type TransferID int64

type Digest [sha256.Size]byte

func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}
