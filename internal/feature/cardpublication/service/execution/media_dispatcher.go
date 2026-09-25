package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
)

const mediaClassifierVersion = 1

type MediaUploadMethod string

const (
	MediaUploadByLinks MediaUploadMethod = "links"
	MediaUploadByFile  MediaUploadMethod = "file"
)

type DirectMediaTransport interface {
	DownloadMediaFile(context.Context, string) (contentapi.DownloadedMediaFile, error)
	UploadMediaFile(
		context.Context,
		CabinetID,
		domain.ClientGeneration,
		contentapi.UploadMediaFileRequest,
	) (contentapi.UploadMediaFileResponse, error)
}

var ErrMediaActionConflict = fmt.Errorf(
	"publication media action changed: %w",
	core_errors.ErrConflict,
)

type MediaActionCandidate = ProductActionCandidate

type MediaAction struct {
	ProductActionCandidate
	PlanID                 int64
	Revision               int64
	State                  string
	RequestDigest          Digest
	RequestPayload         []byte
	MemberSetDigest        Digest
	MediaLinkSetRoot       Digest
	SellerKey              domain.SellerKey
	ClientGeneration       domain.ClientGeneration
	CredentialExpiresAt    time.Time
	PreflightObservationID int64
	Member                 ProductActionMember
	AttributionID          int64
	NMID                   int64
	IdentityRevision       int64
	AttemptID              int64
	RecheckObservationID   int64
	AttemptRequestDigest   Digest
	AttemptRequestPayload  []byte
	AttemptStartedAt       time.Time
}

type mediaTemplate struct {
	Links []string `json:"links"`
}

func (action MediaAction) Validate() error {
	if action.TransferID <= 0 || action.ActionID <= 0 || action.TargetID <= 0 ||
		action.CabinetID == "" || action.AuthorizationID <= 0 ||
		action.AuthorizationRevision < 0 || action.PlanDigest == (Digest{}) ||
		action.TargetSetRoot == (Digest{}) || action.PlanID <= 0 || action.Revision < 0 ||
		action.RequestDigest == (Digest{}) || len(action.RequestPayload) == 0 ||
		action.MemberSetDigest == (Digest{}) || action.MediaLinkSetRoot == (Digest{}) ||
		action.SellerKey == (domain.SellerKey{}) ||
		action.ClientGeneration == (domain.ClientGeneration{}) ||
		action.CredentialExpiresAt.IsZero() || action.PreflightObservationID <= 0 ||
		action.Member.ID <= 0 || action.Member.GroupTargetID <= 0 ||
		action.Member.TransferItemTargetID <= 0 ||
		action.Member.RequestMemberIndex != 0 || action.Member.VendorCode == "" ||
		strings.TrimSpace(action.Member.VendorCode) != action.Member.VendorCode ||
		action.AttributionID < 0 || action.NMID <= 0 || action.IdentityRevision < 0 ||
		(action.State != "planned" && action.State != "dispatching" && action.State != "reconciling") {
		return errors.New("publication media action is invalid")
	}
	var template mediaTemplate
	if err := decodeExactJSON(action.RequestPayload, &template); err != nil ||
		len(template.Links) == 0 || len(template.Links) > contentapi.MaxMediaLinks {
		return errors.New("publication media template is invalid")
	}
	for _, link := range template.Links {
		if link == "" {
			return errors.New("publication media template contains an empty link")
		}
	}
	if digestParts("cardpublication-media-template:v1", action.RequestPayload) !=
		action.RequestDigest ||
		digestParts("cardpublication-media-links:v1", action.RequestPayload) !=
			action.MediaLinkSetRoot ||
		digestMembers([]ActionMemberDraft{{
			GroupTargetID:        action.Member.GroupTargetID,
			TransferItemTargetID: action.Member.TransferItemTargetID,
			RequestMemberIndex:   action.Member.RequestMemberIndex,
			VendorCode:           action.Member.VendorCode,
		}}) != action.MemberSetDigest {
		return errors.New("publication media action digest differs")
	}
	if action.AttemptID == 0 {
		if action.State != "planned" || action.RecheckObservationID != 0 ||
			action.AttemptRequestDigest != (Digest{}) ||
			len(action.AttemptRequestPayload) != 0 || !action.AttemptStartedAt.IsZero() {
			return errors.New("publication media action has a partial attempt")
		}
		return nil
	}
	if (action.State != "dispatching" && action.State != "reconciling") || action.RecheckObservationID <= 0 ||
		action.AttemptRequestDigest == (Digest{}) ||
		len(action.AttemptRequestPayload) == 0 || action.AttemptStartedAt.IsZero() {
		return errors.New("publication media attempt is incomplete")
	}
	request, payload, digest, err := action.FinalRequest()
	if err != nil || request.NMID != action.NMID || digest != action.AttemptRequestDigest ||
		!bytes.Equal(payload, action.AttemptRequestPayload) {
		return errors.New("publication media attempt request differs")
	}
	return nil
}

func (action MediaAction) FinalRequest() (
	contentapi.SaveMediaByLinksRequest,
	[]byte,
	Digest,
	error,
) {
	var template mediaTemplate
	if err := decodeExactJSON(action.RequestPayload, &template); err != nil {
		return contentapi.SaveMediaByLinksRequest{}, nil, Digest{}, err
	}
	request := contentapi.SaveMediaByLinksRequest{
		NMID: action.NMID,
		Data: append([]string(nil), template.Links...),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return contentapi.SaveMediaByLinksRequest{}, nil, Digest{}, err
	}
	return request, payload, digestParts("cardpublication-media-request:v1", payload), nil
}

type MediaAttempt struct {
	ID                   int64
	TransferID           transfer_service.TransferID
	ActionID             int64
	AuthorizationID      transfer_service.LiveAuthorizationID
	RecheckObservationID int64
	AttributionID        int64
	RequestDigest        Digest
	RequestPayload       []byte
	StartedAt            time.Time
}

type BeginMediaAttemptCommand struct {
	Action               MediaAction
	Authorization        transfer_service.LiveAuthorizationEvidence
	Baseline             ErrorBaseline
	RecheckObservationID int64
	RequestDigest        Digest
	RequestPayload       []byte
}

type MediaMutationResult struct {
	Delivery          SubmissionDelivery
	HTTPStatus        int
	ClassifierVersion int
	Disposition       SubmissionDisposition
	OutcomeClass      transfer_service.ResultClass
	OutcomeCode       string
	UnmatchedCount    int
}

func (result MediaMutationResult) Validate() error {
	if result.ClassifierVersion <= 0 || result.OutcomeCode == "" ||
		len(result.OutcomeCode) > 128 || result.UnmatchedCount < 0 ||
		!result.OutcomeClass.IsValid() || result.OutcomeClass == transfer_service.ResultPartial {
		return errors.New("publication media mutation result is invalid")
	}
	switch result.Delivery {
	case SubmissionNotDispatched:
		if result.HTTPStatus != 0 || result.Disposition != SubmissionRejectedProven ||
			result.OutcomeClass != transfer_service.ResultInternalError {
			return errors.New("not-dispatched media result is invalid")
		}
	case SubmissionResponseReceived:
		if result.HTTPStatus != 0 && (result.HTTPStatus < 100 || result.HTTPStatus > 599) {
			return errors.New("publication media response status is invalid")
		}
	case SubmissionUnknownDelivery:
		if result.HTTPStatus != 0 || result.Disposition != SubmissionUncertain ||
			result.OutcomeClass != transfer_service.ResultUnresolved {
			return errors.New("unknown-delivery media result is invalid")
		}
	default:
		return errors.New("publication media delivery state is invalid")
	}
	switch result.Disposition {
	case SubmissionAccepted:
		if result.OutcomeClass != transfer_service.ResultSuccess || result.UnmatchedCount != 0 {
			return errors.New("accepted publication media result is invalid")
		}
	case SubmissionRejectedProven:
		if (result.OutcomeClass != transfer_service.ResultRejected &&
			result.OutcomeClass != transfer_service.ResultInternalError) ||
			result.UnmatchedCount != 0 {
			return errors.New("rejected publication media result is invalid")
		}
	case SubmissionUncertain:
		if result.OutcomeClass != transfer_service.ResultUnresolved ||
			result.UnmatchedCount != 1 {
			return errors.New("uncertain publication media result is invalid")
		}
	default:
		return errors.New("publication media disposition is invalid")
	}
	return nil
}

type MediaGroupResult struct {
	Terminal      bool
	GroupTargetID int64
	OutcomeClass  transfer_service.ResultClass
	OutcomeCode   string
}

func (result MediaGroupResult) Validate() error {
	if result.GroupTargetID <= 0 {
		return errors.New("publication media group identity is invalid")
	}
	if !result.Terminal {
		if result.OutcomeClass != "" || result.OutcomeCode != "" {
			return errors.New("pending publication media group has a result")
		}
		return nil
	}
	if !result.OutcomeClass.IsValid() || result.OutcomeClass == transfer_service.ResultPartial ||
		result.OutcomeCode == "" || len(result.OutcomeCode) > 128 {
		return errors.New("terminal publication media group result is invalid")
	}
	return nil
}

type MediaJournalRepository interface {
	ScheduleMediaVisibility(context.Context, core_postgres_transaction.DBTX, MediaAction, MediaAttempt, MediaMutationResult, time.Duration, time.Duration) error
	ClaimMediaVisibility(context.Context, int, time.Duration) ([]MediaVisibilityJob, error)
	UpdateMediaVisibility(context.Context, core_postgres_transaction.DBTX, MediaAction, MediaVisibilityJob, MediaVisibilityUpdate) (MediaGroupResult, error)
	ListDispatchableMediaActions(context.Context, int) ([]MediaActionCandidate, error)
	ListPendingMediaActions(context.Context, int) ([]MediaActionCandidate, error)
	ListInterruptedMediaActions(context.Context, int) ([]MediaActionCandidate, error)
	LockMediaAction(
		context.Context,
		core_postgres_transaction.DBTX,
		MediaActionCandidate,
	) (MediaAction, error)
	InsertActionObservation(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_service.TransferID,
		string,
		ObservationDraft,
	) (int64, error)
	BeginMediaAttempt(
		context.Context,
		core_postgres_transaction.DBTX,
		BeginMediaAttemptCommand,
	) (MediaAttempt, error)
	FinishMediaWithoutAttempt(
		context.Context,
		core_postgres_transaction.DBTX,
		MediaAction,
		string,
	) (MediaGroupResult, error)
	RecordMediaResult(
		context.Context,
		core_postgres_transaction.DBTX,
		MediaAction,
		MediaAttempt,
		MediaMutationResult,
	) (MediaGroupResult, error)
	PublicationPlanTerminal(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_service.TransferID,
		int64,
	) (bool, error)
}

type MediaDispatcher struct {
	repository         MediaJournalRepository
	transport          CatalogTransport
	catalogReader      *CatalogReader
	errorFeed          *ErrorFeed
	authorization      LiveAuthorizationVerifier
	transferResults    PublicationExecutionResultApplier
	uow                core_postgres_transaction.UnitOfWork
	enabled            bool
	mediaUploadMethod  MediaUploadMethod
	mediaCheckInterval time.Duration
	mediaCheckTimeout  time.Duration
	concurrency        int
	processMu          sync.Mutex
	visibilityMu       sync.Mutex
	inFlightMu         sync.Mutex
	inFlight           map[mediaActionKey]struct{}
	inFlightWait       sync.WaitGroup
	asyncErrors        chan error
	logger             *zap.Logger
}

type mediaActionKey struct {
	transferID transfer_service.TransferID
	actionID   int64
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
	effectiveMediaCheckInterval := max(mediaCheckInterval, time.Minute)
	if repository == nil || transport == nil || catalogReader == nil || errorFeed == nil ||
		authorization == nil || transferResults == nil || uow == nil ||
		(mediaUploadMethod != MediaUploadByLinks && mediaUploadMethod != MediaUploadByFile) ||
		mediaCheckInterval <= 0 || mediaCheckTimeout <= effectiveMediaCheckInterval || concurrency <= 0 {
		panic("cardpublication media dispatcher dependency is nil")
	}
	return &MediaDispatcher{
		repository:         repository,
		transport:          transport,
		catalogReader:      catalogReader,
		errorFeed:          errorFeed,
		authorization:      authorization,
		transferResults:    transferResults,
		uow:                uow,
		enabled:            enabled,
		mediaUploadMethod:  mediaUploadMethod,
		mediaCheckInterval: effectiveMediaCheckInterval,
		mediaCheckTimeout:  mediaCheckTimeout,
		concurrency:        concurrency,
		inFlight:           make(map[mediaActionKey]struct{}),
		asyncErrors:        make(chan error, max(concurrency*4, 32)),
		logger:             core_observability.Logger(loggers...),
	}
}

func (dispatcher *MediaDispatcher) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	var interruptedDuration, listDuration, processDuration, lockWait time.Duration
	interruptedCount, candidatesCount, scheduledCount := 0, 0, 0
	inFlightCount := 0
	defer func() {
		logTiming := core_observability.LogTimingDebug
		if interruptedCount > 0 || candidatesCount > 0 {
			logTiming = core_observability.LogTiming
		}
		logTiming(
			dispatcher.logger,
			"cardpublication",
			"media_dispatch_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Bool("enabled", dispatcher.enabled),
			zap.Int("interrupted_actions_count", interruptedCount),
			zap.Int("candidate_actions_count", candidatesCount),
			zap.Int("scheduled_actions_count", scheduledCount),
			zap.Int("in_flight_actions_count", inFlightCount),
			zap.Int("concurrency", dispatcher.concurrency),
			zap.Duration("interrupted_duration", interruptedDuration),
			zap.Duration("list_candidates_duration", listDuration),
			zap.Duration("process_duration", processDuration),
		)
	}()
	if ctx == nil {
		return errors.New("process pending publication media: context is nil")
	}
	lockStartedAt := time.Now()
	dispatcher.processMu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer dispatcher.processMu.Unlock()

	firstErr := dispatcher.drainAsyncErrors()
	stepStartedAt := time.Now()
	interrupted, err := dispatcher.repository.ListInterruptedMediaActions(
		ctx,
		productDispatchPageSize,
	)
	if err != nil {
		interruptedDuration = time.Since(stepStartedAt)
		return err
	}
	interruptedCount = len(interrupted)
	for _, candidate := range interrupted {
		if dispatcher.mediaActionInFlight(candidate) {
			continue
		}
		if err := dispatcher.recordInterrupted(ctx, candidate); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	interruptedDuration = time.Since(stepStartedAt)
	var candidates []MediaActionCandidate
	stepStartedAt = time.Now()
	if dispatcher.enabled {
		candidates, err = dispatcher.repository.ListDispatchableMediaActions(
			ctx,
			productDispatchPageSize,
		)
	} else {
		candidates, err = dispatcher.repository.ListPendingMediaActions(
			ctx,
			productDispatchPageSize,
		)
	}
	if err != nil {
		listDuration = time.Since(stepStartedAt)
		return errors.Join(firstErr, err)
	}
	listDuration = time.Since(stepStartedAt)
	candidatesCount = len(candidates)
	stepStartedAt = time.Now()
	for _, candidate := range candidates {
		if !dispatcher.reserveMediaAction(candidate) {
			continue
		}
		scheduledCount++
		dispatcher.inFlightWait.Add(1)
		go dispatcher.processReservedMediaAction(ctx, candidate)
	}
	processDuration = time.Since(stepStartedAt)
	inFlightCount = dispatcher.mediaActionsInFlight()
	return firstErr
}

func (dispatcher *MediaDispatcher) processReservedMediaAction(
	ctx context.Context,
	candidate MediaActionCandidate,
) {
	defer dispatcher.inFlightWait.Done()
	defer dispatcher.releaseMediaAction(candidate)
	var err error
	if dispatcher.enabled {
		err = dispatcher.dispatch(ctx, candidate)
	} else {
		err = dispatcher.skip(ctx, candidate, "MEDIA_AUTO_DISPATCH_DISABLED")
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	err = fmt.Errorf(
		"process publication media action ID='%d': %w",
		candidate.ActionID,
		err,
	)
	select {
	case dispatcher.asyncErrors <- err:
	default:
		dispatcher.logger.Error("Publication media worker failed", zap.Error(err))
	}
}

func (dispatcher *MediaDispatcher) reserveMediaAction(candidate MediaActionCandidate) bool {
	key := mediaActionKey{transferID: candidate.TransferID, actionID: candidate.ActionID}
	dispatcher.inFlightMu.Lock()
	defer dispatcher.inFlightMu.Unlock()
	if dispatcher.inFlight == nil {
		dispatcher.inFlight = make(map[mediaActionKey]struct{})
	}
	if dispatcher.asyncErrors == nil {
		dispatcher.asyncErrors = make(chan error, max(dispatcher.concurrency*4, 32))
	}
	if len(dispatcher.inFlight) >= dispatcher.concurrency {
		return false
	}
	if _, exists := dispatcher.inFlight[key]; exists {
		return false
	}
	dispatcher.inFlight[key] = struct{}{}
	return true
}

func (dispatcher *MediaDispatcher) releaseMediaAction(candidate MediaActionCandidate) {
	key := mediaActionKey{transferID: candidate.TransferID, actionID: candidate.ActionID}
	dispatcher.inFlightMu.Lock()
	delete(dispatcher.inFlight, key)
	dispatcher.inFlightMu.Unlock()
}

func (dispatcher *MediaDispatcher) mediaActionInFlight(candidate MediaActionCandidate) bool {
	key := mediaActionKey{transferID: candidate.TransferID, actionID: candidate.ActionID}
	dispatcher.inFlightMu.Lock()
	_, exists := dispatcher.inFlight[key]
	dispatcher.inFlightMu.Unlock()
	return exists
}

func (dispatcher *MediaDispatcher) mediaActionsInFlight() int {
	dispatcher.inFlightMu.Lock()
	count := len(dispatcher.inFlight)
	dispatcher.inFlightMu.Unlock()
	return count
}

func (dispatcher *MediaDispatcher) drainAsyncErrors() error {
	if dispatcher.asyncErrors == nil {
		return nil
	}
	var result error
	for {
		select {
		case err := <-dispatcher.asyncErrors:
			result = errors.Join(result, err)
		default:
			return result
		}
	}
}

// Wait lets the feature drain result writes before the database pool closes.
func (dispatcher *MediaDispatcher) Wait() {
	dispatcher.inFlightWait.Wait()
}

func (dispatcher *MediaDispatcher) dispatch(
	ctx context.Context,
	candidate MediaActionCandidate,
) (err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(candidate.TransferID), ActionID: candidate.ActionID,
	})
	core_observability.LogStarted(dispatcher.logger, "cardpublication", "dispatch_media_action",
		zap.Int64("transfer_id", int64(candidate.TransferID)), zap.Int64("action_id", candidate.ActionID),
		zap.String("cabinet_id", string(candidate.CabinetID)))
	var (
		loadActionDuration     time.Duration
		catalogDuration        time.Duration
		buildDuration          time.Duration
		beginAttemptDuration   time.Duration
		mutationDuration       time.Duration
		recordResultDuration   time.Duration
		nmID                   int64
		linksCount             int
		result                 MediaMutationResult
		finishedWithoutAttempt bool
		recheckCode            string
	)
	defer func() {
		core_observability.LogTiming(
			dispatcher.logger,
			"cardpublication",
			"dispatch_media_action",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(candidate.TransferID)),
			zap.Int64("action_id", candidate.ActionID),
			zap.Int64("target_id", candidate.TargetID),
			zap.String("cabinet_id", string(candidate.CabinetID)),
			zap.Int64("nm_id", nmID),
			zap.Int("links_count", linksCount),
			zap.String("delivery", string(result.Delivery)),
			zap.String("disposition", string(result.Disposition)),
			zap.Int("http_status", result.HTTPStatus),
			zap.String("outcome_class", string(result.OutcomeClass)),
			zap.String("outcome_code", result.OutcomeCode),
			zap.Bool("finished_without_attempt", finishedWithoutAttempt),
			zap.String("recheck_code", recheckCode),
			zap.Duration("load_action_duration", loadActionDuration),
			zap.Duration("catalog_read_duration", catalogDuration),
			zap.Duration("build_request_duration", buildDuration),
			zap.Duration("begin_attempt_duration", beginAttemptDuration),
			zap.Duration("mutation_duration", mutationDuration),
			zap.Duration("record_result_duration", recordResultDuration),
		)
	}()
	stepStartedAt := time.Now()
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	loadActionDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	nmID = action.NMID
	stepStartedAt = time.Now()
	observation, err := dispatcher.catalogReader.ReadActiveVendorCode(
		ctx,
		action.TargetID,
		action.CabinetID,
		action.Member.VendorCode,
	)
	catalogDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	stepStartedAt = time.Now()
	recheck, err := BuildMediaRecheck(action, observation)
	recheckCode = recheck.SafeCode
	if err != nil {
		buildDuration = time.Since(stepStartedAt)
		return err
	}
	request, requestPayload, requestDigest, err := action.FinalRequest()
	buildDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	linksCount = len(request.Data)

	var attempt MediaAttempt
	stepStartedAt = time.Now()
	err = dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			locked, err := dispatcher.repository.LockMediaAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if !sameImmutableMediaAction(action, locked) || locked.State != "planned" {
				return ErrMediaActionConflict
			}
			recheckID, err := dispatcher.repository.InsertActionObservation(
				ctx,
				tx,
				locked.TransferID,
				"targeted_recheck",
				recheck.Observation,
			)
			if err != nil {
				return err
			}
			if !recheck.Stable {
				group, err := dispatcher.repository.FinishMediaWithoutAttempt(
					ctx,
					tx,
					locked,
					recheck.SafeCode,
				)
				if err != nil {
					return err
				}
				if err := dispatcher.transferResults.BeginMediaWithin(
					ctx,
					tx,
					locked.TransferID,
				); err != nil {
					return err
				}
				if err := dispatcher.applyGroupResult(ctx, tx, locked.TransferID, group); err != nil {
					return err
				}
				if err := dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, locked); err != nil {
					return err
				}
				finishedWithoutAttempt = true
				return nil
			}
			authorization, err := dispatcher.authorization.LockValid(
				ctx,
				tx,
				transfer_service.LiveAuthorizationCheck{
					TransferID:                    locked.TransferID,
					ActionID:                      locked.ActionID,
					AuthorizationID:               locked.AuthorizationID,
					ExpectedAuthorizationRevision: locked.AuthorizationRevision,
					PlanDigest:                    transfer_service.Digest(locked.PlanDigest),
					TargetSetRoot:                 transfer_service.Digest(locked.TargetSetRoot),
					TargetID:                      locked.TargetID,
					CabinetID:                     transfer_service.CabinetID(locked.CabinetID),
					SellerKey:                     locked.SellerKey,
					ClientGeneration:              locked.ClientGeneration,
				},
			)
			if err != nil {
				return err
			}
			baseline, err := dispatcher.errorFeed.CaptureBaseline(
				ctx,
				tx,
				CaptureErrorBaselineCommand{
					TransferID: locked.TransferID,
					ActionID:   locked.ActionID,
					CabinetID:  locked.CabinetID,
				},
			)
			if err != nil {
				return err
			}
			attempt, err = dispatcher.repository.BeginMediaAttempt(
				ctx,
				tx,
				BeginMediaAttemptCommand{
					Action:               locked,
					Authorization:        authorization,
					Baseline:             baseline,
					RecheckObservationID: recheckID,
					RequestDigest:        requestDigest,
					RequestPayload:       requestPayload,
				},
			)
			if err != nil {
				return err
			}
			return dispatcher.transferResults.BeginMediaWithin(
				ctx,
				tx,
				locked.TransferID,
			)
		},
	)
	beginAttemptDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	if finishedWithoutAttempt {
		return nil
	}
	if attempt.ID == 0 {
		return ErrMediaActionConflict
	}
	stepStartedAt = time.Now()
	mutationCtx, cancelMutation := context.WithDeadline(ctx, action.CredentialExpiresAt)
	result = executeMediaMutation(
		mutationCtx,
		dispatcher.transport,
		action.CabinetID,
		action.ClientGeneration,
		request,
		dispatcher.mediaUploadMethod,
	)
	cancelMutation()
	mutationDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	err = dispatcher.recordResult(resultCtx, candidate, attempt, result)
	recordResultDuration = time.Since(stepStartedAt)
	return err
}

func (dispatcher *MediaDispatcher) skip(
	ctx context.Context,
	candidate MediaActionCandidate,
	safeCode string,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockMediaAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			group, err := dispatcher.repository.FinishMediaWithoutAttempt(
				ctx,
				tx,
				action,
				safeCode,
			)
			if err != nil {
				return err
			}
			if err := dispatcher.transferResults.BeginMediaWithin(
				ctx,
				tx,
				action.TransferID,
			); err != nil {
				return err
			}
			if err := dispatcher.applyGroupResult(ctx, tx, action.TransferID, group); err != nil {
				return err
			}
			return dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, action)
		},
	)
}

func (dispatcher *MediaDispatcher) recordInterrupted(
	ctx context.Context,
	candidate MediaActionCandidate,
) error {
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	if err != nil {
		return err
	}
	if action.State != "dispatching" || action.AttemptID <= 0 {
		return ErrMediaActionConflict
	}
	return dispatcher.recordResult(
		ctx,
		candidate,
		mediaAttemptFromAction(action),
		MediaMutationResult{
			Delivery:          SubmissionUnknownDelivery,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionUncertain,
			OutcomeClass:      transfer_service.ResultUnresolved,
			OutcomeCode:       "MEDIA_PROCESS_INTERRUPTED_AFTER_ATTEMPT_COMMIT",
			UnmatchedCount:    1,
		},
	)
}

func (dispatcher *MediaDispatcher) recordResult(
	ctx context.Context,
	candidate MediaActionCandidate,
	attempt MediaAttempt,
	result MediaMutationResult,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockMediaAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if action.State != "dispatching" || action.AttemptID != attempt.ID {
				return ErrMediaActionConflict
			}
			attempt = mediaAttemptFromAction(action)
			if result.Disposition == SubmissionAccepted || result.Disposition == SubmissionUncertain {
				return dispatcher.repository.ScheduleMediaVisibility(ctx, tx, action, attempt, result,
					dispatcher.mediaCheckInterval, dispatcher.mediaCheckTimeout)
			}
			group, err := dispatcher.repository.RecordMediaResult(
				ctx,
				tx,
				action,
				attempt,
				result,
			)
			if err != nil {
				return err
			}
			if err := dispatcher.applyGroupResult(ctx, tx, action.TransferID, group); err != nil {
				return err
			}
			return dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, action)
		},
	)
}

func (dispatcher *MediaDispatcher) applyGroupResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	group MediaGroupResult,
) error {
	if err := group.Validate(); err != nil {
		return err
	}
	if !group.Terminal {
		return nil
	}
	return dispatcher.transferResults.ApplyMediaWithin(
		ctx,
		tx,
		transfer_service.ApplyPublicationMediaResultCommand{
			TransferID:    transferID,
			GroupTargetID: group.GroupTargetID,
			OutcomeClass:  group.OutcomeClass,
			OutcomeCode:   group.OutcomeCode,
		},
	)
}

func (dispatcher *MediaDispatcher) closeAuthorizationIfPlanTerminal(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action MediaAction,
) error {
	terminal, err := dispatcher.repository.PublicationPlanTerminal(
		ctx,
		tx,
		action.TransferID,
		action.PlanID,
	)
	if err != nil || !terminal {
		return err
	}
	_, err = dispatcher.authorization.CloseWithin(
		ctx,
		tx,
		transfer_service.ChangeLiveAuthorizationStateCommand{
			TransferID:         action.TransferID,
			AuthorizationID:    action.AuthorizationID,
			ExpectedRevision:   action.AuthorizationRevision,
			ExpectedPlanDigest: transfer_service.Digest(action.PlanDigest),
			SafeReasonCode:     "publication_plan_terminal",
		},
	)
	return err
}

func (dispatcher *MediaDispatcher) loadLockedAction(
	ctx context.Context,
	candidate MediaActionCandidate,
) (MediaAction, error) {
	var action MediaAction
	err := dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			action, err = dispatcher.repository.LockMediaAction(ctx, tx, candidate)
			return err
		},
	)
	return action, err
}

func BuildMediaRecheck(
	action MediaAction,
	observation CatalogObservation,
) (TargetedRecheck, error) {
	if err := action.Validate(); err != nil {
		return TargetedRecheck{}, err
	}
	if observation.TargetID != action.TargetID || observation.CabinetID != action.CabinetID {
		return TargetedRecheck{}, ErrMediaActionConflict
	}
	normal := make([]contentapi.Card, 0, 1)
	trash := make([]contentapi.TrashCard, 0, 1)
	for _, card := range observation.Normal {
		if card.VendorCode == action.Member.VendorCode {
			normal = append(normal, card)
		}
	}
	for _, card := range observation.Trash {
		if card.VendorCode == action.Member.VendorCode {
			trash = append(trash, card)
		}
	}
	targeted := CatalogObservation{
		TargetID:   observation.TargetID,
		CabinetID:  observation.CabinetID,
		Normal:     normal,
		Trash:      trash,
		ObservedAt: observation.ObservedAt,
	}
	draft, err := observationDraft(targeted)
	if err != nil {
		return TargetedRecheck{}, err
	}
	stable := len(normal) == 1 && len(trash) == 0 && normal[0].NMID == action.NMID
	safeCode := "MEDIA_ATTRIBUTION_RECHECK_STABLE"
	if !stable {
		safeCode = "MEDIA_ATTRIBUTION_RECHECK_FAILED"
	}
	return TargetedRecheck{Observation: draft, Stable: stable, SafeCode: safeCode}, nil
}

func executeMediaMutation(
	ctx context.Context,
	transport CatalogTransport,
	cabinetID CabinetID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
	method MediaUploadMethod,
) MediaMutationResult {
	if method == MediaUploadByFile {
		return executeDirectMediaMutation(ctx, transport, cabinetID, generation, request)
	}
	response, err := retryMediaRequest(ctx, waitMediaRetry, func() (contentapi.SaveMediaByLinksResponse, error) {
		return transport.SaveMediaByLinks(ctx, cabinetID, generation, request)
	})
	if err != nil {
		return classifyMediaError(err)
	}
	if response.Error {
		return MediaMutationResult{
			Delivery:          SubmissionResponseReceived,
			HTTPStatus:        200,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			OutcomeClass:      transfer_service.ResultRejected,
			OutcomeCode:       "WB_MEDIA_ENVELOPE_REJECTED",
		}
	}
	return MediaMutationResult{
		Delivery:          SubmissionResponseReceived,
		HTTPStatus:        200,
		ClassifierVersion: mediaClassifierVersion,
		Disposition:       SubmissionAccepted,
		OutcomeClass:      transfer_service.ResultSuccess,
		OutcomeCode:       "WB_MEDIA_REQUEST_ACCEPTED",
	}
}

func executeDirectMediaMutation(
	ctx context.Context,
	transport CatalogTransport,
	cabinetID CabinetID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) MediaMutationResult {
	return executeDirectMediaMutationWithWait(ctx, transport, cabinetID, generation, request, waitMediaRetry)
}

func executeDirectMediaMutationWithWait(
	ctx context.Context,
	transport CatalogTransport,
	cabinetID CabinetID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
	wait func(context.Context, time.Duration) error,
) MediaMutationResult {
	directTransport, ok := transport.(DirectMediaTransport)
	if !ok {
		return MediaMutationResult{
			Delivery:          SubmissionNotDispatched,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			OutcomeClass:      transfer_service.ResultInternalError,
			OutcomeCode:       "WB_MEDIA_FILE_TRANSPORT_UNAVAILABLE",
		}
	}
	accepted := 0
	photoNumber := 0
	for _, link := range request.Data {
		file, err := retryMediaRequest(ctx, wait, func() (contentapi.DownloadedMediaFile, error) {
			return directTransport.DownloadMediaFile(ctx, link)
		})
		if err != nil {
			if accepted > 0 {
				return partialMediaMutationResult()
			}
			return classifyMediaError(err)
		}
		mediaNumber := 1
		if !strings.HasPrefix(file.MediaType, "video/") {
			photoNumber++
			mediaNumber = photoNumber
		}
		upload := contentapi.UploadMediaFileRequest{
			NMID: request.NMID, MediaNumber: mediaNumber,
			FileName: file.FileName, Data: file.Data,
		}
		response, err := retryMediaRequest(ctx, wait, func() (contentapi.UploadMediaFileResponse, error) {
			return directTransport.UploadMediaFile(ctx, cabinetID, generation, upload)
		})
		if err != nil {
			if accepted > 0 {
				return partialMediaMutationResult()
			}
			return classifyMediaError(err)
		}
		if response.Error {
			if accepted > 0 {
				return partialMediaMutationResult()
			}
			return MediaMutationResult{
				Delivery: SubmissionResponseReceived, HTTPStatus: 200,
				ClassifierVersion: mediaClassifierVersion,
				Disposition:       SubmissionRejectedProven,
				OutcomeClass:      transfer_service.ResultRejected,
				OutcomeCode:       "WB_MEDIA_ENVELOPE_REJECTED",
			}
		}
		accepted++
	}
	return MediaMutationResult{
		Delivery: SubmissionResponseReceived, HTTPStatus: 200,
		ClassifierVersion: mediaClassifierVersion,
		Disposition:       SubmissionAccepted,
		OutcomeClass:      transfer_service.ResultSuccess,
		OutcomeCode:       "WB_MEDIA_REQUEST_ACCEPTED",
	}
}

func partialMediaMutationResult() MediaMutationResult {
	return MediaMutationResult{
		Delivery:          SubmissionUnknownDelivery,
		ClassifierVersion: mediaClassifierVersion,
		Disposition:       SubmissionUncertain,
		OutcomeClass:      transfer_service.ResultUnresolved,
		OutcomeCode:       "WB_MEDIA_PARTIAL_UPLOAD",
		UnmatchedCount:    1,
	}
}

func classifyMediaError(err error) MediaMutationResult {
	var classified core_wb.ClassifiedError
	if !errors.As(err, &classified) {
		return MediaMutationResult{
			Delivery:          SubmissionNotDispatched,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			OutcomeClass:      transfer_service.ResultInternalError,
			OutcomeCode:       "WB_MEDIA_CALL_NOT_DISPATCHED",
		}
	}
	safeCode := "WB_MEDIA_TRANSPORT_" + strings.ToUpper(classified.Code())
	switch classified.Delivery() {
	case core_wb.NotDispatched:
		return MediaMutationResult{
			Delivery:          SubmissionNotDispatched,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			OutcomeClass:      transfer_service.ResultInternalError,
			OutcomeCode:       safeCode,
		}
	case core_wb.ResponseReceived:
		return MediaMutationResult{
			Delivery:          SubmissionResponseReceived,
			HTTPStatus:        classified.HTTPStatus(),
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionUncertain,
			OutcomeClass:      transfer_service.ResultUnresolved,
			OutcomeCode:       safeCode,
			UnmatchedCount:    1,
		}
	case core_wb.UnknownDelivery:
		return MediaMutationResult{
			Delivery:          SubmissionUnknownDelivery,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionUncertain,
			OutcomeClass:      transfer_service.ResultUnresolved,
			OutcomeCode:       safeCode,
			UnmatchedCount:    1,
		}
	default:
		return MediaMutationResult{
			Delivery:          SubmissionNotDispatched,
			ClassifierVersion: mediaClassifierVersion,
			Disposition:       SubmissionRejectedProven,
			OutcomeClass:      transfer_service.ResultInternalError,
			OutcomeCode:       "WB_MEDIA_DELIVERY_STATE_INVALID",
		}
	}
}

func sameImmutableMediaAction(left, right MediaAction) bool {
	return left.TransferID == right.TransferID && left.ActionID == right.ActionID &&
		left.PlanID == right.PlanID && left.TargetID == right.TargetID &&
		left.CabinetID == right.CabinetID && left.PlanDigest == right.PlanDigest &&
		left.TargetSetRoot == right.TargetSetRoot &&
		left.RequestDigest == right.RequestDigest &&
		left.MemberSetDigest == right.MemberSetDigest &&
		left.MediaLinkSetRoot == right.MediaLinkSetRoot &&
		left.SellerKey == right.SellerKey &&
		left.ClientGeneration == right.ClientGeneration &&
		left.CredentialExpiresAt.Equal(right.CredentialExpiresAt) &&
		left.PreflightObservationID == right.PreflightObservationID &&
		left.Member == right.Member && left.AttributionID == right.AttributionID &&
		left.NMID == right.NMID && bytes.Equal(left.RequestPayload, right.RequestPayload)
}

func mediaAttemptFromAction(action MediaAction) MediaAttempt {
	return MediaAttempt{
		ID:                   action.AttemptID,
		TransferID:           action.TransferID,
		ActionID:             action.ActionID,
		AuthorizationID:      action.AuthorizationID,
		RecheckObservationID: action.RecheckObservationID,
		AttributionID:        action.AttributionID,
		RequestDigest:        action.AttemptRequestDigest,
		RequestPayload:       append([]byte(nil), action.AttemptRequestPayload...),
		StartedAt:            action.AttemptStartedAt,
	}
}
