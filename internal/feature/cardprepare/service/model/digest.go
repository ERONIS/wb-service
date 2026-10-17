package model

import "github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"

func DigestJSON(domain string, value any) (Digest, error) {
	return cardpipeline.DigestJSON(domain, value)
}

func DigestProposal(semanticDigest, metadataDigest Digest, request []byte) Digest {
	return cardpipeline.DigestProposal(semanticDigest, metadataDigest, request)
}
