// Package cardpipeline contains stable value objects shared by the card
// import, preparation, transfer, publication and reporting features.
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
