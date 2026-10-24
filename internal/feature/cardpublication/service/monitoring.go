package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"

	"go.uber.org/zap"
)

var ErrErrorFeedEnvelope = monitoring.ErrErrorFeedEnvelope

func NewErrorFeed(
	transport cardpublication_catalog.CatalogTransport,
	repository monitoring.ErrorFeedRepository,
	targets monitoring.ErrorTargetSource,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *monitoring.ErrorFeed {
	return monitoring.NewErrorFeed(transport, repository, targets, uow, loggers...)
}
