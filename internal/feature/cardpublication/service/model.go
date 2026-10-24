package cardpublication_service

import "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"

const (
	ActionCreateGroup = model.ActionCreateGroup
	ActionAddToGroup  = model.ActionAddToGroup
	ActionUploadMedia = model.ActionUploadMedia

	DecisionCreateGroup              = model.DecisionCreateGroup
	DecisionAddToGroup               = model.DecisionAddToGroup
	DecisionAlreadyPresentCompatible = model.DecisionAlreadyPresentCompatible
	DecisionAlreadyPresentDifferent  = model.DecisionAlreadyPresentDifferent
	DecisionCardInTrashConflict      = model.DecisionCardInTrashConflict
	DecisionGroupConflict            = model.DecisionGroupConflict
	DecisionSubjectConflict          = model.DecisionSubjectConflict
	DecisionGroupCapacityConflict    = model.DecisionGroupCapacityConflict
	DecisionRemoteIdentityConflict   = model.DecisionRemoteIdentityConflict

	IdentityRemoteMissing  = model.IdentityRemoteMissing
	IdentityRemotePresent  = model.IdentityRemotePresent
	IdentityRemoteConflict = model.IdentityRemoteConflict
)
