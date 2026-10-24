package workflow

import (
	"github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
)

const (
	OutcomePrepared                     = model.OutcomePrepared
	OutcomeTargetTemporarilyUnavailable = model.OutcomeTargetTemporarilyUnavailable
	OutcomeCatalogContractError         = model.OutcomeCatalogContractError
)

var ErrPreparationMismatch = model.ErrPreparationMismatch
var ErrProposalNotFound = model.ErrProposalNotFound
