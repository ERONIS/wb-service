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
	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"

	"go.uber.org/zap"
)

const mediaClassifierVersion = 1

var ErrMediaActionConflict = core_errors.ErrConflict

type MediaAction struct {
	ProductActionCandidate
	Revision               int64
	State                  string
	RequestPayload         []byte
	SellerID               domain.SellerID
	ClientGeneration       domain.ClientGeneration
	CredentialExpiresAt    time.Time
	PreflightObservationID int64
	Member                 ProductActionMember
	AttributionID          int64
	NMID                   int64
	IdentityRevision       int64
	AttemptID              int64
	RecheckObservationID   int64
	AttemptRequestPayload  []byte
	AttemptStartedAt       time.Time
}

type mediaTemplate struct {
	Links []string `json:"links"`
}

func (action MediaAction) Validate() error {
	if action.TransferID <= 0 || action.ActionID <= 0 || action.TargetID <= 0 ||
		action.SellerID.Validate() != nil || action.AuthorizationID <= 0 ||
		action.AuthorizationRevision < 0 || action.PlanID <= 0 || action.Revision < 0 ||
		len(action.RequestPayload) == 0 ||
		action.ClientGeneration.IsZero() ||
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
	if action.AttemptID == 0 {
		if action.State != "planned" || action.RecheckObservationID != 0 ||
			len(action.AttemptRequestPayload) != 0 || !action.AttemptStartedAt.IsZero() {
			return errors.New("publication media action has a partial attempt")
		}
		return nil
	}
	if (action.State != "dispatching" && action.State != "reconciling") || action.RecheckObservationID <= 0 ||
		len(action.AttemptRequestPayload) == 0 || action.AttemptStartedAt.IsZero() {
		return errors.New("publication media attempt is incomplete")
	}
	request, _, err := action.FinalRequest()
	if err != nil || request.NMID != action.NMID {
		return errors.New("publication media attempt request is invalid")
	}
	return nil
}

func (action MediaAction) FinalRequest() (
	contentapi.SaveMediaByLinksRequest,
	[]byte,
	error,
) {
	var template mediaTemplate
	if err := decodeExactJSON(action.RequestPayload, &template); err != nil {
		return contentapi.SaveMediaByLinksRequest{}, nil, err
	}
	request := contentapi.SaveMediaByLinksRequest{
		NMID: action.NMID,
		Data: append([]string(nil), template.Links...),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return contentapi.SaveMediaByLinksRequest{}, nil, err
	}
	return request, payload, nil
}

type MediaAttempt struct {
	ID                   int64
	TransferID           transfer_model.TransferID
	ActionID             int64
	AuthorizationID      transfer_authorization.LiveAuthorizationID
	RecheckObservationID int64
	AttributionID        int64
	RequestPayload       []byte
	StartedAt            time.Time
}

type BeginMediaAttemptCommand struct {
	Action               MediaAction
	Authorization        transfer_authorization.LiveAuthorizationEvidence
	Baseline             cardpublication_monitoring.ErrorBaseline
	RecheckObservationID int64
	RequestPayload       []byte
}

type MediaMutationResult struct {
	Delivery          SubmissionDelivery
	HTTPStatus        int
	ClassifierVersion int
	Disposition       SubmissionDisposition
	OutcomeClass      transfer_model.ResultClass
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
	OutcomeClass  transfer_model.ResultClass
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
	ListDispatchableMediaActions(context.Context, int) ([]ProductActionCandidate, error)
	ListPendingMediaActions(context.Context, int) ([]ProductActionCandidate, error)
	ListInterruptedMediaActions(context.Context, int) ([]ProductActionCandidate, error)
	LockMediaAction(
		context.Context,
		core_postgres_transaction.DBTX,
		ProductActionCandidate,
	) (MediaAction, error)
	InsertActionObservation(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_model.TransferID,
		string,
		cardpublication_model.ObservationDraft,
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
		transfer_model.TransferID,
		int64,
	) (bool, error)
}

type MediaDispatcher struct {
	repository         MediaJournalRepository
	transport          cardpublication_catalog.CatalogTransport
	catalogReader      *cardpublication_catalog.CatalogReader
	errorFeed          *cardpublication_monitoring.ErrorFeed
	authorization      LiveAuthorizationVerifier
	transferResults    PublicationExecutionResultApplier
	uow                core_postgres_transaction.UnitOfWork
	enabled            bool
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
	transferID transfer_model.TransferID
	actionID   int64
}

func NewMediaDispatcher(
	repository MediaJournalRepository,
	transport cardpublication_catalog.CatalogTransport,
	catalogReader *cardpublication_catalog.CatalogReader,
	errorFeed *cardpublication_monitoring.ErrorFeed,
	authorization LiveAuthorizationVerifier,
	transferResults PublicationExecutionResultApplier,
	uow core_postgres_transaction.UnitOfWork,
	enabled bool,
	mediaCheckInterval time.Duration,
	mediaCheckTimeout time.Duration,
	concurrency int,
	loggers ...*zap.Logger,
) *MediaDispatcher {
	effectiveMediaCheckInterval := max(mediaCheckInterval, time.Minute)
	if repository == nil || transport == nil || catalogReader == nil || errorFeed == nil ||
		authorization == nil || transferResults == nil || uow == nil ||
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
		mediaCheckInterval: effectiveMediaCheckInterval,
		mediaCheckTimeout:  mediaCheckTimeout,
		concurrency:        concurrency,
		inFlight:           make(map[mediaActionKey]struct{}),
		asyncErrors:        make(chan error, max(concurrency*4, 32)),
		logger:             core_logger.Fallback(loggers...),
	}
}

func (dispatcher *MediaDispatcher) ProcessPending(ctx context.Context) (err error) {
	if ctx == nil {
		return errors.New("process pending publication media: context is nil")
	}
	dispatcher.processMu.Lock()
	defer dispatcher.processMu.Unlock()

	firstErr := dispatcher.drainAsyncErrors()
	interrupted, err := dispatcher.repository.ListInterruptedMediaActions(
		ctx,
		productDispatchPageSize,
	)
	if err != nil {
		return err
	}
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
	var candidates []ProductActionCandidate
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
		return errors.Join(firstErr, err)
	}
	for _, candidate := range candidates {
		if !dispatcher.reserveMediaAction(candidate) {
			continue
		}
		dispatcher.inFlightWait.Add(1)
		go dispatcher.processReservedMediaAction(ctx, candidate)
	}
	return firstErr
}

func (dispatcher *MediaDispatcher) processReservedMediaAction(
	ctx context.Context,
	candidate ProductActionCandidate,
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

func (dispatcher *MediaDispatcher) reserveMediaAction(candidate ProductActionCandidate) bool {
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

func (dispatcher *MediaDispatcher) releaseMediaAction(candidate ProductActionCandidate) {
	key := mediaActionKey{transferID: candidate.TransferID, actionID: candidate.ActionID}
	dispatcher.inFlightMu.Lock()
	delete(dispatcher.inFlight, key)
	dispatcher.inFlightMu.Unlock()
}

func (dispatcher *MediaDispatcher) mediaActionInFlight(candidate ProductActionCandidate) bool {
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

func (dispatcher *MediaDispatcher) Wait() {
	dispatcher.inFlightWait.Wait()
}

func (dispatcher *MediaDispatcher) dispatch(
	ctx context.Context,
	candidate ProductActionCandidate,
) error {
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	if err != nil {
		return err
	}
	observation, err := dispatcher.catalogReader.ReadActiveVendorCode(
		ctx,
		action.TargetID,
		action.SellerID,
		action.Member.VendorCode,
	)
	if err != nil {
		return err
	}
	recheck, err := BuildMediaRecheck(action, observation)
	if err != nil {
		return err
	}
	request, requestPayload, err := action.FinalRequest()
	if err != nil {
		return err
	}

	var attempt MediaAttempt
	var finishedWithoutAttempt bool
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
				transfer_authorization.LiveAuthorizationCheck{
					TransferID:                    locked.TransferID,
					ActionID:                      locked.ActionID,
					AuthorizationID:               locked.AuthorizationID,
					ExpectedAuthorizationRevision: locked.AuthorizationRevision,
					PlanID:                        locked.PlanID,
					TargetID:                      locked.TargetID,
					SellerID:                      locked.SellerID,
					ClientGeneration:              locked.ClientGeneration,
				},
			)
			if err != nil {
				return err
			}
			baseline, err := dispatcher.errorFeed.CaptureBaseline(
				ctx,
				tx,
				cardpublication_monitoring.CaptureErrorBaselineCommand{
					TransferID: locked.TransferID,
					ActionID:   locked.ActionID,
					SellerID:   locked.SellerID,
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
	if err != nil {
		return err
	}
	if finishedWithoutAttempt {
		return nil
	}
	if attempt.ID == 0 {
		return ErrMediaActionConflict
	}
	mutationCtx, cancelMutation := context.WithDeadline(ctx, action.CredentialExpiresAt)
	result := executeMediaMutation(
		mutationCtx,
		dispatcher.transport,
		action.SellerID,
		action.ClientGeneration,
		request,
	)
	cancelMutation()
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	return dispatcher.recordResult(resultCtx, candidate, attempt, result)
}

func (dispatcher *MediaDispatcher) skip(
	ctx context.Context,
	candidate ProductActionCandidate,
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
	candidate ProductActionCandidate,
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
	candidate ProductActionCandidate,
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
	transferID transfer_model.TransferID,
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
		transfer_publication.ApplyPublicationMediaResultCommand{
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
	return closeTerminalPlanAuthorization(
		ctx, tx, dispatcher.repository, dispatcher.authorization,
		action.ProductActionCandidate, action.PlanID,
	)
}

func (dispatcher *MediaDispatcher) loadLockedAction(
	ctx context.Context,
	candidate ProductActionCandidate,
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
	observation cardpublication_catalog.CatalogObservation,
) (TargetedRecheck, error) {
	if err := action.Validate(); err != nil {
		return TargetedRecheck{}, err
	}
	if observation.TargetID != action.TargetID || observation.SellerID != action.SellerID {
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
	targeted := cardpublication_catalog.CatalogObservation{
		TargetID:   observation.TargetID,
		SellerID:   observation.SellerID,
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
	transport cardpublication_catalog.CatalogTransport,
	sellerID domain.SellerID,
	generation domain.ClientGeneration,
	request contentapi.SaveMediaByLinksRequest,
) MediaMutationResult {
	response, err := transport.SaveMediaByLinks(ctx, sellerID, generation, request)
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

func classifyMediaError(err error) MediaMutationResult {
	var classified core_wb.ClassifiedError
	if !errors.As(err, &classified) ||
		(classified.Delivery() == core_wb.NotDispatched &&
			classified.Code() == core_wb.ErrorCodeExecutorUnavailable) {
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
		left.SellerID == right.SellerID &&
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
		RequestPayload:       append([]byte(nil), action.AttemptRequestPayload...),
		StartedAt:            action.AttemptStartedAt,
	}
}
