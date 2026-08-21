package transfer_service

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

func TargetSetRoot(snapshot MutationTargetSnapshot) Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-target-set:v1")
	writeString(hasher, snapshot.CohortName)
	writeBytes(hasher, snapshot.Revision[:])
	writeInt64(hasher, int64(len(snapshot.Targets)))
	for _, target := range snapshot.Targets {
		writeInt64(hasher, int64(target.Position))
		writeString(hasher, string(target.CabinetID))
		writeBytes(hasher, target.SellerKey[:])
		writeInt64(hasher, target.BindingRevision)
		writeInt64(hasher, target.CapabilityRevision)
		writeBool(hasher, target.ContentRead)
		writeBool(hasher, target.ContentWrite)
	}

	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

func writeString(writer hash.Hash, value string) {
	writeBytes(writer, []byte(value))
}

func writeBytes(writer hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func writeInt64(writer hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = writer.Write(encoded[:])
}

func writeBool(writer hash.Hash, value bool) {
	if value {
		writeBytes(writer, []byte{1})
		return
	}
	writeBytes(writer, []byte{0})
}
