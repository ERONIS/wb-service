package cardprepare_service

import "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"

type Digest = model.Digest
type CabinetID = model.CabinetID
type OutcomeCode = model.OutcomeCode
type Outcome = model.Outcome
type SourceGroup = model.SourceGroup
type SourceItem = model.SourceItem
type CatalogSnapshot = model.CatalogSnapshot
type DirectorySnapshot = model.DirectorySnapshot
type Proposal = model.Proposal
type Result = model.Result
type PreparationID = model.PreparationID
type PreparationGroupID = model.PreparationGroupID
type StartPreparationCommand = model.StartPreparationCommand
type ProposalQuery = model.ProposalQuery
type PreparedMember = model.PreparedMember
type StoredProposal = model.StoredProposal

const (
	OutcomePrepared                     = model.OutcomePrepared
	OutcomeSubjectNotFound              = model.OutcomeSubjectNotFound
	OutcomeSubjectAmbiguous             = model.OutcomeSubjectAmbiguous
	OutcomeCharacteristicMissing        = model.OutcomeCharacteristicMissing
	OutcomeCharacteristicInvalid        = model.OutcomeCharacteristicInvalid
	OutcomeBrandNotFound                = model.OutcomeBrandNotFound
	OutcomeDirectoryValueInvalid        = model.OutcomeDirectoryValueInvalid
	OutcomeCardLimitExceeded            = model.OutcomeCardLimitExceeded
	OutcomePayloadTooLarge              = model.OutcomePayloadTooLarge
	OutcomeTargetTemporarilyUnavailable = model.OutcomeTargetTemporarilyUnavailable
	OutcomeCatalogContractError         = model.OutcomeCatalogContractError
)

var ErrPreparationMismatch = model.ErrPreparationMismatch
var ErrProposalNotFound = model.ErrProposalNotFound
