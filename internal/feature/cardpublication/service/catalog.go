package cardpublication_service

import (
	"time"

	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"go.uber.org/zap"
)

type CatalogTransport = catalog.CatalogTransport
type CatalogObservation = catalog.CatalogObservation
type CatalogReader = catalog.CatalogReader
type RecentCardsCache = catalog.RecentCardsCache

var ErrCatalogChangedDuringScan = catalog.ErrCatalogChangedDuringScan

func NewCatalogReader(transport CatalogTransport, loggers ...*zap.Logger) *CatalogReader {
	return catalog.NewCatalogReader(transport, loggers...)
}

func NewRecentCardsCache(ttl time.Duration) *RecentCardsCache {
	return catalog.NewRecentCardsCache(ttl)
}
