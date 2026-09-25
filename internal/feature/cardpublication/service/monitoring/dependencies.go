package monitoring

import (
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
)

type CabinetID = model.CabinetID
type Digest = model.Digest
type CatalogTransport = catalog.CatalogTransport

func digestParts(domain string, parts ...[]byte) Digest {
	return model.DigestParts(domain, parts...)
}

var normalizedUniqueStrings = model.NormalizeUniqueStrings
var parseWBTime = model.ParseWBTime
