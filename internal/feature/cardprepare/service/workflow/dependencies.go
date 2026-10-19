package workflow

import (
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
)

type Digest = model.Digest
type SellerID = model.SellerID
type OutcomeCode = model.OutcomeCode
type Outcome = model.Outcome
type Result = model.Result
type SourceGroup = model.SourceGroup
type SourceItem = model.SourceItem
type ProposalQuery = model.ProposalQuery
type PreparedMember = model.PreparedMember
type StoredProposal = model.StoredProposal
type Preparer = preparation.Preparer

const (
	OutcomePrepared                     = model.OutcomePrepared
	OutcomeTargetTemporarilyUnavailable = model.OutcomeTargetTemporarilyUnavailable
	OutcomeCatalogContractError         = model.OutcomeCatalogContractError
)

var ErrPreparationMismatch = model.ErrPreparationMismatch
var ErrProposalNotFound = model.ErrProposalNotFound
