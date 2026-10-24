package cardpublication_service

import (
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"

	"go.uber.org/zap"
)

const (
	SubmissionNotDispatched    = execution.SubmissionNotDispatched
	SubmissionResponseReceived = execution.SubmissionResponseReceived
	SubmissionUnknownDelivery  = execution.SubmissionUnknownDelivery

	SubmissionAccepted       = execution.SubmissionAccepted
	SubmissionRejectedProven = execution.SubmissionRejectedProven
	SubmissionUncertain      = execution.SubmissionUncertain
)

var (
	ErrProductActionConflict = execution.ErrProductActionConflict
	ErrProductPlanSuperseded = execution.ErrProductPlanSuperseded
	ErrProductPlanStopped    = execution.ErrProductPlanStopped
	ErrMediaActionConflict   = execution.ErrMediaActionConflict
)

func NewProductDispatcher(
	repository execution.ProductJournalRepository,
	transport cardpublication_catalog.CatalogTransport,
	catalogReader *cardpublication_catalog.CatalogReader,
	errorFeed *cardpublication_monitoring.ErrorFeed,
	authorization execution.LiveAuthorizationVerifier,
	transferResults execution.PublicationExecutionResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	reconciliationDelay time.Duration,
	reconciliationTimeout time.Duration,
	concurrency int,
	loggers ...*zap.Logger,
) *execution.ProductDispatcher {
	return execution.NewProductDispatcher(
		repository,
		transport,
		catalogReader,
		errorFeed,
		authorization,
		transferResults,
		uow,
		reconciliationDelay,
		reconciliationTimeout,
		concurrency,
		loggers...,
	)
}

func NewMediaDispatcher(
	repository execution.MediaJournalRepository,
	transport cardpublication_catalog.CatalogTransport,
	catalogReader *cardpublication_catalog.CatalogReader,
	errorFeed *cardpublication_monitoring.ErrorFeed,
	authorization execution.LiveAuthorizationVerifier,
	transferResults execution.PublicationExecutionResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	enabled bool,
	mediaCheckInterval time.Duration,
	mediaCheckTimeout time.Duration,
	concurrency int,
	loggers ...*zap.Logger,
) *execution.MediaDispatcher {
	return execution.NewMediaDispatcher(
		repository,
		transport,
		catalogReader,
		errorFeed,
		authorization,
		transferResults,
		uow,
		enabled,
		mediaCheckInterval,
		mediaCheckTimeout,
		concurrency,
		loggers...,
	)
}

func BuildTargetedRecheck(
	action execution.ProductAction,
	observation cardpublication_catalog.CatalogObservation,
) (execution.TargetedRecheck, error) {
	return execution.BuildTargetedRecheck(action, observation)
}

func BuildMediaRecheck(
	action execution.MediaAction,
	observation cardpublication_catalog.CatalogObservation,
) (execution.TargetedRecheck, error) {
	return execution.BuildMediaRecheck(action, observation)
}

func ValidateSubmissionResult(result execution.SubmissionResult) error {
	return execution.ValidateSubmissionResult(result)
}

func ValidateMemberReconciliationResults(
	action execution.ProductAction,
	results []execution.MemberReconciliationResult,
) error {
	return execution.ValidateMemberReconciliationResults(action, results)
}
