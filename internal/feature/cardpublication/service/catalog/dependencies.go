package catalog

import "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"

type CabinetID = model.CabinetID
type Digest = model.Digest
type ObservationDraft = model.ObservationDraft

func digestParts(domain string, parts ...[]byte) Digest {
	return model.DigestParts(domain, parts...)
}

var normalizedUniqueStrings = model.NormalizeUniqueStrings
var parseWBTime = model.ParseWBTime
