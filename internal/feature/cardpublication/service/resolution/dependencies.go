package resolution

import "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"

const (
	ActionCreateGroup = model.ActionCreateGroup
	ActionAddToGroup  = model.ActionAddToGroup
)

func decodeExactJSON(payload []byte, target any) error {
	return model.DecodeExactJSON(payload, target)
}
