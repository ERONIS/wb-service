package model

import (
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

var (
	ErrPreparationMismatch = core_errors.ErrConflict
	ErrProposalNotFound    = core_errors.ErrNotFound
)

type ProposalQuery struct {
	TransferID    transfer_service.TransferID
	GroupTargetID int64
	ProposalRoot  Digest
}

// Validate проверяет корректность параметров запроса proposal.
func (query ProposalQuery) Validate() error {
	if query.TransferID <= 0 || query.GroupTargetID <= 0 || query.ProposalRoot == (Digest{}) {
		return core_errors.InvalidArgument("card preparation proposal query is invalid")
	}
	return nil
}

type PreparedMember struct {
	TransferItemID int64
	ItemPosition   int
	VendorCode     string
}

type StoredProposal struct {
	TransferID    transfer_service.TransferID
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
	SellerID      SellerID
	Proposal      Proposal
	Members       []PreparedMember
}
