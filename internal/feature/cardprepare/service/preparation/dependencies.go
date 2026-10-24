package preparation

import "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"

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
