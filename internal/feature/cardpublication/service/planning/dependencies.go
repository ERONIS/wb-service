package planning

import (
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
)

type CabinetID = model.CabinetID
type Digest = model.Digest
type ActionKind = model.ActionKind
type DecisionCode = model.DecisionCode
type IdentityState = model.IdentityState
type ObservationDraft = model.ObservationDraft
type ActionMemberDraft = model.ActionMemberDraft
type ActionDraft = model.ActionDraft
type PlannedItemDraft = model.PlannedItemDraft
type PlannedGroupDraft = model.PlannedGroupDraft
type IdentityDraft = model.IdentityDraft
type PlanDraft = model.PlanDraft
type PersistedAction = model.PersistedAction
type PersistedPlan = model.PersistedPlan

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

type CatalogObservation = catalog.CatalogObservation
type CatalogReader = catalog.CatalogReader

func observationDraft(observation CatalogObservation) (ObservationDraft, error) {
	return catalog.DraftObservation(observation)
}

func digestParts(domain string, parts ...[]byte) Digest {
	return model.DigestParts(domain, parts...)
}

func digestMembers(members []ActionMemberDraft) Digest {
	return model.DigestMembers(members)
}

func decodeExactJSON(payload []byte, target any) error {
	return model.DecodeExactJSON(payload, target)
}
