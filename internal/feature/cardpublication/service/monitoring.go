package cardpublication_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	"go.uber.org/zap"
)

type ErrorTargetSource = monitoring.ErrorTargetSource
type ErrorFeedRepository = monitoring.ErrorFeedRepository
type ErrorCursor = monitoring.ErrorCursor
type ErrorBatchEvidence = monitoring.ErrorBatchEvidence
type SaveErrorFeedCommand = monitoring.SaveErrorFeedCommand
type CaptureErrorBaselineCommand = monitoring.CaptureErrorBaselineCommand
type ErrorBaseline = monitoring.ErrorBaseline
type ErrorFeed = monitoring.ErrorFeed

var ErrErrorFeedEnvelope = monitoring.ErrErrorFeedEnvelope

func NewErrorFeed(
	transport CatalogTransport,
	repository ErrorFeedRepository,
	targets ErrorTargetSource,
	uow core_postgres_transaction.UnitOfWork,
	loggers ...*zap.Logger,
) *ErrorFeed {
	return monitoring.NewErrorFeed(transport, repository, targets, uow, loggers...)
}
