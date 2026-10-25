package preparation

import (
	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

var (
	ErrPreparationMismatch = core_errors.ErrConflict
	ErrProposalNotFound    = core_errors.ErrNotFound
)

type ProposalQuery struct {
	TransferID    transfer_model.TransferID
	GroupTargetID int64
}

// Validate проверяет корректность параметров запроса proposal.
func (query ProposalQuery) Validate() error {
	if query.TransferID <= 0 || query.GroupTargetID <= 0 {
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
	TransferID    transfer_model.TransferID
	GroupTargetID int64
	SourceGroupID int64
	TargetID      int64
	SellerID      core_domain.SellerID
	Proposal      cardprepare_model.Proposal
	Members       []PreparedMember
}
