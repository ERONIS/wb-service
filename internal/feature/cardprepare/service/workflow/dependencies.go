package workflow

import (
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/preparation"
)

type Digest = model.Digest
type CabinetID = model.CabinetID
type OutcomeCode = model.OutcomeCode
type Outcome = model.Outcome
type Result = model.Result
type SourceGroup = model.SourceGroup
type SourceItem = model.SourceItem
type PreparationID = model.PreparationID
type PreparationGroupID = model.PreparationGroupID
type StartPreparationCommand = model.StartPreparationCommand
type ProposalQuery = model.ProposalQuery
type PreparedMember = model.PreparedMember
type StoredProposal = model.StoredProposal
type Preparer = preparation.Preparer

const (
	OutcomePrepared                     = model.OutcomePrepared
	OutcomeTargetTemporarilyUnavailable = model.OutcomeTargetTemporarilyUnavailable
)

var ErrPreparationMismatch = model.ErrPreparationMismatch
var ErrProposalNotFound = model.ErrProposalNotFound
