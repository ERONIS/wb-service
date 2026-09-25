package execution

import (
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
)

type CabinetID = model.CabinetID
type Digest = model.Digest
type ActionKind = model.ActionKind
type ObservationDraft = model.ObservationDraft
type ActionMemberDraft = model.ActionMemberDraft

const (
	ActionCreateGroup = model.ActionCreateGroup
	ActionAddToGroup  = model.ActionAddToGroup
	ActionUploadMedia = model.ActionUploadMedia
)

type CatalogObservation = catalog.CatalogObservation
type CatalogReader = catalog.CatalogReader
type CatalogTransport = catalog.CatalogTransport

type ErrorFeed = monitoring.ErrorFeed
type CaptureErrorBaselineCommand = monitoring.CaptureErrorBaselineCommand
type ErrorBaseline = monitoring.ErrorBaseline

func observationDraft(observation CatalogObservation) (ObservationDraft, error) {
	return catalog.DraftObservation(observation)
}

func decodeExactJSON(payload []byte, target any) error {
	return model.DecodeExactJSON(payload, target)
}

func digestParts(domain string, parts ...[]byte) Digest {
	return model.DigestParts(domain, parts...)
}

func digestMembers(members []ActionMemberDraft) Digest {
	return model.DigestMembers(members)
}
