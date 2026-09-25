package model

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ValidateIntegrity verifies the immutable parts of a proposal without any WB
// or database access. SemanticDigest cannot be reconstructed without the
// source cards, but it is still covered by ProposalRoot.
func (proposal Proposal) ValidateIntegrity() error {
	if proposal.SubjectID <= 0 || len(proposal.Request) != 1 ||
		len(proposal.Request[0].Variants) == 0 ||
		proposal.Request[0].SubjectID != proposal.SubjectID ||
		len(proposal.EncodedRequest) == 0 ||
		proposal.SemanticDigest == (Digest{}) ||
		proposal.MetadataDigest == (Digest{}) ||
		proposal.ProposalRoot == (Digest{}) ||
		proposal.LimitsObservedAt.IsZero() ||
		proposal.MetadataSnapshot.ObservedAt.IsZero() ||
		!proposal.LimitsObservedAt.Equal(proposal.MetadataSnapshot.ObservedAt) ||
		proposal.Limits != proposal.MetadataSnapshot.Limits {
		return errors.New("card preparation proposal shape is invalid")
	}
	encoded, err := json.Marshal(proposal.Request)
	if err != nil || !bytes.Equal(encoded, proposal.EncodedRequest) {
		return errors.New("card preparation proposal request bytes differ")
	}
	metadataDigest, err := DigestJSON(
		"cardprepare-metadata:v1",
		proposal.MetadataSnapshot,
	)
	if err != nil || metadataDigest != proposal.MetadataDigest {
		return errors.New("card preparation proposal metadata digest differs")
	}
	root := DigestProposal(
		proposal.SemanticDigest,
		proposal.MetadataDigest,
		proposal.EncodedRequest,
	)
	if root != proposal.ProposalRoot {
		return errors.New("card preparation proposal root differs")
	}
	return nil
}
