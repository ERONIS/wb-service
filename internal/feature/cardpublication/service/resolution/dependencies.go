package resolution

import "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"

type CabinetID = model.CabinetID
type Digest = model.Digest
type ActionKind = model.ActionKind

const (
	ActionCreateGroup = model.ActionCreateGroup
	ActionAddToGroup  = model.ActionAddToGroup
)

func decodeExactJSON(payload []byte, target any) error {
	return model.DecodeExactJSON(payload, target)
}

func digestParts(domain string, parts ...[]byte) Digest {
	return model.DigestParts(domain, parts...)
}
