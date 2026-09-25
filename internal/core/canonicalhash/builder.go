// Package canonicalhash provides the stable length-prefixed SHA-256 encoding
// used for persisted identities, idempotency keys and workflow roots.
package canonicalhash

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

type Builder struct {
	hash hash.Hash
}

func NewSHA256(domain string) *Builder {
	builder := &Builder{hash: sha256.New()}
	builder.String(domain)
	return builder
}

func (builder *Builder) String(value string) {
	builder.Bytes([]byte(value))
}

func (builder *Builder) Bytes(value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = builder.hash.Write(size[:])
	_, _ = builder.hash.Write(value)
}

func (builder *Builder) Int64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = builder.hash.Write(encoded[:])
}

func (builder *Builder) Bool(value bool) {
	if value {
		builder.Bytes([]byte{1})
		return
	}
	builder.Bytes([]byte{0})
}

func (builder *Builder) Sum() [sha256.Size]byte {
	var result [sha256.Size]byte
	copy(result[:], builder.hash.Sum(nil))
	return result
}
