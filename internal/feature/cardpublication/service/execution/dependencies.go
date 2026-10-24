package execution

import (
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
)

const (
	ActionCreateGroup = model.ActionCreateGroup
	ActionAddToGroup  = model.ActionAddToGroup
	ActionUploadMedia = model.ActionUploadMedia
)

func observationDraft(observation catalog.CatalogObservation) (model.ObservationDraft, error) {
	return catalog.DraftObservation(observation)
}

func decodeExactJSON(payload []byte, target any) error {
	return model.DecodeExactJSON(payload, target)
}
