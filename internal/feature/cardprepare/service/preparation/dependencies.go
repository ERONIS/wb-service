package preparation

import "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"

type Digest = model.Digest
type CabinetID = model.CabinetID
type OutcomeCode = model.OutcomeCode
type Outcome = model.Outcome
type OutcomeDetails = model.OutcomeDetails
type SourceGroup = model.SourceGroup
type SourceItem = model.SourceItem
type CatalogSnapshot = model.CatalogSnapshot
type DirectorySnapshot = model.DirectorySnapshot
type Proposal = model.Proposal
type Result = model.Result

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

func digestJSON(domain string, value any) (Digest, error) {
	return model.DigestJSON(domain, value)
}

func digestProposal(semanticDigest, metadataDigest Digest, request []byte) Digest {
	return model.DigestProposal(semanticDigest, metadataDigest, request)
}
