package model

import (
	"errors"
)

func (proposal Proposal) Validate() error {
	if proposal.SubjectID <= 0 || len(proposal.Request) != 1 ||
		len(proposal.Request[0].Variants) == 0 ||
		proposal.Request[0].SubjectID != proposal.SubjectID ||
		len(proposal.EncodedRequest) == 0 ||
		proposal.LimitsObservedAt.IsZero() ||
		proposal.MetadataSnapshot.ObservedAt.IsZero() ||
		!proposal.LimitsObservedAt.Equal(proposal.MetadataSnapshot.ObservedAt) ||
		proposal.Limits != proposal.MetadataSnapshot.Limits {
		return errors.New("card preparation proposal shape is invalid")
	}
	return nil
}
