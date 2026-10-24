package cardpublication_service

import (
	"time"

	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"go.uber.org/zap"
)

var ErrCatalogChangedDuringScan = catalog.ErrCatalogChangedDuringScan

func NewCatalogReader(transport catalog.CatalogTransport, loggers ...*zap.Logger) *catalog.CatalogReader {
	return catalog.NewCatalogReader(transport, loggers...)
}

func NewRecentCardsCache(ttl time.Duration) *catalog.RecentCardsCache {
	return catalog.NewRecentCardsCache(ttl)
}
