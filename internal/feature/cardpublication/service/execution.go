package cardpublication_service

import (
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	"go.uber.org/zap"
)

type ProductActionCandidate = execution.ProductActionCandidate
type ProductActionMember = execution.ProductActionMember
type AbortedProductAction = execution.AbortedProductAction
type ProductAction = execution.ProductAction
type TargetedRecheck = execution.TargetedRecheck
type ProductAttempt = execution.ProductAttempt
type ProductRetry = execution.ProductRetry
type SubmissionDelivery = execution.SubmissionDelivery
type SubmissionDisposition = execution.SubmissionDisposition
type MemberSubmissionResult = execution.MemberSubmissionResult
type SubmissionResult = execution.SubmissionResult
type BeginProductAttemptCommand = execution.BeginProductAttemptCommand
type RecordSubmissionCommand = execution.RecordSubmissionCommand
type BeginProductRetryCommand = execution.BeginProductRetryCommand
type RecordProductRetryCommand = execution.RecordProductRetryCommand
type ErrorBatchMatch = execution.ErrorBatchMatch
type MemberReconciliationResult = execution.MemberReconciliationResult
type ProductJournalRepository = execution.ProductJournalRepository
type PublicationExecutionResultApplier = execution.PublicationExecutionResultApplier
type LiveAuthorizationVerifier = execution.LiveAuthorizationVerifier
type ProductDispatcher = execution.ProductDispatcher

type MediaActionCandidate = execution.MediaActionCandidate
type MediaAction = execution.MediaAction
type MediaAttempt = execution.MediaAttempt
type BeginMediaAttemptCommand = execution.BeginMediaAttemptCommand
type MediaMutationResult = execution.MediaMutationResult
type MediaVisibilityJob = execution.MediaVisibilityJob
type MediaVisibilityUpdate = execution.MediaVisibilityUpdate
type MediaGroupResult = execution.MediaGroupResult
type MediaJournalRepository = execution.MediaJournalRepository
type MediaDispatcher = execution.MediaDispatcher
type MediaUploadMethod = execution.MediaUploadMethod

const (
	SubmissionNotDispatched    = execution.SubmissionNotDispatched
	SubmissionResponseReceived = execution.SubmissionResponseReceived
	SubmissionUnknownDelivery  = execution.SubmissionUnknownDelivery

	SubmissionAccepted       = execution.SubmissionAccepted
	SubmissionRejectedProven = execution.SubmissionRejectedProven
	SubmissionUncertain      = execution.SubmissionUncertain

	MediaUploadByLinks = execution.MediaUploadByLinks
	MediaUploadByFile  = execution.MediaUploadByFile
)

var (
	ErrProductActionConflict = execution.ErrProductActionConflict
	ErrProductPlanSuperseded = execution.ErrProductPlanSuperseded
	ErrProductPlanStopped    = execution.ErrProductPlanStopped
	ErrMediaActionConflict   = execution.ErrMediaActionConflict
)

func NewProductDispatcher(
	repository ProductJournalRepository,
	transport CatalogTransport,
	catalogReader *CatalogReader,
	errorFeed *ErrorFeed,
	authorization LiveAuthorizationVerifier,
	transferResults PublicationExecutionResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	reconciliationDelay time.Duration,
	reconciliationTimeout time.Duration,
	concurrency int,
	loggers ...*zap.Logger,
) *ProductDispatcher {
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
	repository MediaJournalRepository,
	transport CatalogTransport,
	catalogReader *CatalogReader,
	errorFeed *ErrorFeed,
	authorization LiveAuthorizationVerifier,
	transferResults PublicationExecutionResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	enabled bool,
	mediaUploadMethod MediaUploadMethod,
	mediaCheckInterval time.Duration,
	mediaCheckTimeout time.Duration,
	concurrency int,
	loggers ...*zap.Logger,
) *MediaDispatcher {
	return execution.NewMediaDispatcher(
		repository,
		transport,
		catalogReader,
		errorFeed,
		authorization,
		transferResults,
		uow,
		enabled,
		mediaUploadMethod,
		mediaCheckInterval,
		mediaCheckTimeout,
		concurrency,
		loggers...,
	)
}

func BuildTargetedRecheck(
	action ProductAction,
	observation CatalogObservation,
) (TargetedRecheck, error) {
	return execution.BuildTargetedRecheck(action, observation)
}

func BuildMediaRecheck(
	action MediaAction,
	observation CatalogObservation,
) (TargetedRecheck, error) {
	return execution.BuildMediaRecheck(action, observation)
}

func ValidateSubmissionResult(result SubmissionResult) error {
	return execution.ValidateSubmissionResult(result)
}

func ValidateMemberReconciliationResults(
	action ProductAction,
	results []MemberReconciliationResult,
) error {
	return execution.ValidateMemberReconciliationResults(action, results)
}
