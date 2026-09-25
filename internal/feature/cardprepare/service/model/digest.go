package model

import (
	"crypto/sha256"
	"encoding/json"
)

func DigestJSON(domain string, value any) (Digest, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Digest{}, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(domain))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(encoded)
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func DigestProposal(semanticDigest, metadataDigest Digest, request []byte) Digest {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("cardprepare-proposal:v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(semanticDigest[:])
	_, _ = hasher.Write(metadataDigest[:])
	_, _ = hasher.Write(request)
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest
}
