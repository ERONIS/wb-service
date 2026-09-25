package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
)

const productDispatchPageSize = 20

type ProductDispatcher struct {
	repository            ProductJournalRepository
	transport             CatalogTransport
	catalogReader         *CatalogReader
	errorFeed             *ErrorFeed
	authorization         LiveAuthorizationVerifier
	transferResults       PublicationExecutionResultApplier
	uow                   core_postgres_transaction.UnitOfWork
	reconciliationDelay   time.Duration
	reconciliationTimeout time.Duration
	concurrency           int
	now                   func() time.Time
	logger                *zap.Logger
	processMu             sync.Mutex
}

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
	if repository == nil || transport == nil || catalogReader == nil ||
		errorFeed == nil || authorization == nil || transferResults == nil || uow == nil ||
		reconciliationDelay <= 0 || reconciliationTimeout <= reconciliationDelay || concurrency <= 0 {
		panic("cardpublication product dispatcher dependency is nil")
	}
	return &ProductDispatcher{
		repository:            repository,
		transport:             transport,
		catalogReader:         catalogReader,
		errorFeed:             errorFeed,
		authorization:         authorization,
		transferResults:       transferResults,
		uow:                   uow,
		reconciliationDelay:   reconciliationDelay,
		reconciliationTimeout: reconciliationTimeout,
		concurrency:           concurrency,
		now:                   time.Now,
		logger:                core_observability.Logger(loggers...),
	}
}

func (dispatcher *ProductDispatcher) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	var interruptedDuration, reconciliationDuration, listDuration, dispatchDuration, lockWait time.Duration
	interruptedCount, dispatchableCount := 0, 0
	defer func() {
		logTiming := core_observability.LogTimingDebug
		if interruptedCount > 0 || dispatchableCount > 0 {
			logTiming = core_observability.LogTiming
		}
		logTiming(
			dispatcher.logger,
			"cardpublication",
			"product_dispatch_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Int("interrupted_actions_count", interruptedCount),
			zap.Int("dispatchable_actions_count", dispatchableCount),
			zap.Int("concurrency", dispatcher.concurrency),
			zap.Duration("interrupted_duration", interruptedDuration),
			zap.Duration("reconciliation_duration", reconciliationDuration),
			zap.Duration("list_dispatchable_duration", listDuration),
			zap.Duration("dispatch_duration", dispatchDuration),
		)
	}()
	if ctx == nil {
		return errors.New("process pending product dispatch: context is nil")
	}
	lockStartedAt := time.Now()
	dispatcher.processMu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer dispatcher.processMu.Unlock()

	var firstErr error
	stepStartedAt := time.Now()
	interrupted, err := dispatcher.repository.ListInterruptedProductActions(
		ctx,
		productDispatchPageSize,
	)
	if err != nil {
		interruptedDuration = time.Since(stepStartedAt)
		return err
	}
	interruptedCount = len(interrupted)
	for _, candidate := range interrupted {
		if err := dispatcher.recordInterrupted(ctx, candidate); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	interruptedDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	candidates, err := dispatcher.repository.ListDispatchableProductActions(
		ctx,
		productDispatchPageSize,
	)
	listDuration = time.Since(stepStartedAt)
	if err != nil {
		if firstErr != nil {
			return errors.Join(firstErr, err)
		}
		return err
	}
	dispatchableCount = len(candidates)
	var reconciliationErr, dispatchErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		startedAt := time.Now()
		reconciliationErr = dispatcher.reconcilePending(ctx)
		reconciliationDuration = time.Since(startedAt)
	}()
	go func() {
		defer wait.Done()
		startedAt := time.Now()
		dispatchErr = dispatcher.dispatchCandidates(ctx, candidates)
		dispatchDuration = time.Since(startedAt)
	}()
	wait.Wait()
	return errors.Join(firstErr, reconciliationErr, dispatchErr)
}

func (dispatcher *ProductDispatcher) dispatchCandidates(
	ctx context.Context,
	candidates []ProductActionCandidate,
) error {
	var firstErr error
	for offset := 0; offset < len(candidates); offset += dispatcher.concurrency {
		end := min(offset+dispatcher.concurrency, len(candidates))
		batch := candidates[offset:end]
		observations, catalogErr := dispatcher.readProductCatalog(ctx, batch)
		dispatchErr := processConcurrently(
			ctx,
			batch,
			dispatcher.concurrency,
			func(candidate ProductActionCandidate) error {
				observation, exists := observations[productCatalogKey{
					targetID: candidate.TargetID, cabinetID: candidate.CabinetID,
				}]
				if !exists {
					return nil
				}
				if err := dispatcher.dispatch(ctx, candidate, observation); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if isExpectedProductDispatchStop(err) {
						return nil
					}
					return fmt.Errorf(
						"dispatch publication product action ID='%d': %w",
						candidate.ActionID,
						err,
					)
				}
				return nil
			},
		)
		if err := errors.Join(catalogErr, dispatchErr); err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return firstErr
}

func isExpectedProductDispatchStop(err error) bool {
	return errors.Is(err, ErrProductPlanSuperseded) ||
		errors.Is(err, ErrProductPlanStopped) ||
		errors.Is(err, transfer_service.ErrLiveExpired)
}

func (dispatcher *ProductDispatcher) dispatch(
	ctx context.Context,
	candidate ProductActionCandidate,
	observation CatalogObservation,
) (err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(candidate.TransferID), ActionID: candidate.ActionID,
	})
	core_observability.LogStarted(dispatcher.logger, "cardpublication", "dispatch_product_action",
		zap.Int64("transfer_id", int64(candidate.TransferID)), zap.Int64("action_id", candidate.ActionID),
		zap.String("cabinet_id", string(candidate.CabinetID)))
	var (
		loadActionDuration   time.Duration
		buildRecheckDuration time.Duration
		beginAttemptDuration time.Duration
		mutationDuration     time.Duration
		recordResultDuration time.Duration
		actionKind           ActionKind
		membersCount         int
		result               SubmissionResult
		planSuperseded       bool
		planStopped          bool
		authorizationExpired bool
	)
	defer func() {
		core_observability.LogTiming(
			dispatcher.logger,
			"cardpublication",
			"dispatch_product_action",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(candidate.TransferID)),
			zap.Int64("action_id", candidate.ActionID),
			zap.Int64("target_id", candidate.TargetID),
			zap.String("cabinet_id", string(candidate.CabinetID)),
			zap.String("action_kind", string(actionKind)),
			zap.Int("members_count", membersCount),
			zap.Bool("plan_superseded", planSuperseded),
			zap.Bool("plan_stopped", planStopped),
			zap.Bool("authorization_expired", authorizationExpired),
			zap.Bool("catalog_batch_read", true),
			zap.String("delivery", string(result.Delivery)),
			zap.String("disposition", string(result.Disposition)),
			zap.Int("http_status", result.HTTPStatus),
			zap.String("outcome_code", result.SafeCode),
			zap.Duration("load_action_duration", loadActionDuration),
			zap.Duration("build_recheck_duration", buildRecheckDuration),
			zap.Duration("begin_attempt_duration", beginAttemptDuration),
			zap.Duration("mutation_duration", mutationDuration),
			zap.Duration("record_result_duration", recordResultDuration),
		)
	}()
	stepStartedAt := time.Now()
	snapshot, err := dispatcher.loadLockedAction(ctx, candidate)
	loadActionDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	actionKind = snapshot.Kind
	membersCount = len(snapshot.Members)
	stepStartedAt = time.Now()
	recheck, err := BuildTargetedRecheck(snapshot, observation)
	buildRecheckDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}

	var attempt ProductAttempt
	stepStartedAt = time.Now()
	err = dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if !sameImmutableProductAction(snapshot, action) {
				return ErrProductActionConflict
			}
			recheckObservationID, err := dispatcher.repository.InsertActionObservation(
				ctx,
				tx,
				action.TransferID,
				"targeted_recheck",
				recheck.Observation,
			)
			if err != nil {
				return err
			}
			if !recheck.Stable {
				attemptCount, err := dispatcher.repository.PlanAttemptCount(
					ctx,
					tx,
					action.TransferID,
					action.PlanID,
				)
				if err != nil {
					return err
				}
				if attemptCount != 0 {
					aborted, err := dispatcher.repository.AbortPlanAfterDispatch(
						ctx,
						tx,
						action,
						"PLAN_CHANGED_AFTER_DISPATCH_STARTED",
					)
					if err != nil {
						return err
					}
					for _, abortedAction := range aborted {
						items := make([]transfer_service.PublicationTerminalItem, 0, len(abortedAction.Members))
						groupSet := make(map[int64]struct{})
						for _, member := range abortedAction.Members {
							items = append(items, transfer_service.PublicationTerminalItem{
								GroupTargetID:        member.GroupTargetID,
								TransferItemTargetID: member.TransferItemTargetID,
								OutcomeClass:         transfer_service.ResultUnresolved,
								OutcomeCode:          "PLAN_CHANGED_AFTER_DISPATCH_STARTED",
							})
							groupSet[member.GroupTargetID] = struct{}{}
						}
						groups := make([]int64, 0, len(groupSet))
						for groupTargetID := range groupSet {
							groups = append(groups, groupTargetID)
						}
						if err := dispatcher.transferResults.ApplyWithin(
							ctx,
							tx,
							transfer_service.ApplyPublicationActionResultCommand{
								TransferID:         action.TransferID,
								ActionID:           abortedAction.ActionID,
								Items:              items,
								SkipMediaForGroups: groups,
							},
						); err != nil {
							return err
						}
					}
					if err := dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, action); err != nil {
						return err
					}
					planStopped = true
					return nil
				}
				if _, err := dispatcher.authorization.SupersedeWithin(
					ctx,
					tx,
					transfer_service.ChangeLiveAuthorizationStateCommand{
						TransferID:         action.TransferID,
						AuthorizationID:    action.AuthorizationID,
						ExpectedRevision:   action.AuthorizationRevision,
						ExpectedPlanDigest: transfer_service.Digest(action.PlanDigest),
						SafeReasonCode:     "targeted_recheck_changed",
					},
				); err != nil {
					return err
				}
				if err := dispatcher.repository.SupersedePlanBeforeDispatch(
					ctx,
					tx,
					action,
					recheck.SafeCode,
				); err != nil {
					return err
				}
				if err := dispatcher.transferResults.ResetPlanningWithin(
					ctx,
					tx,
					action.TransferID,
					action.PlanID,
				); err != nil {
					return err
				}
				planSuperseded = true
				return nil
			}

			authorization, err := dispatcher.authorization.LockValid(
				ctx,
				tx,
				transfer_service.LiveAuthorizationCheck{
					TransferID:                    action.TransferID,
					ActionID:                      action.ActionID,
					AuthorizationID:               action.AuthorizationID,
					ExpectedAuthorizationRevision: action.AuthorizationRevision,
					PlanDigest:                    transfer_service.Digest(action.PlanDigest),
					TargetSetRoot:                 transfer_service.Digest(action.TargetSetRoot),
					TargetID:                      action.TargetID,
					CabinetID:                     transfer_service.CabinetID(action.CabinetID),
					SellerKey:                     action.SellerKey,
					ClientGeneration:              action.ClientGeneration,
				},
			)
			if errors.Is(err, transfer_service.ErrLiveExpired) {
				authorizationExpired = true
				return nil
			}
			if err != nil {
				return err
			}
			baseline, err := dispatcher.errorFeed.CaptureBaseline(
				ctx,
				tx,
				CaptureErrorBaselineCommand{
					TransferID: action.TransferID,
					ActionID:   action.ActionID,
					CabinetID:  action.CabinetID,
				},
			)
			if err != nil {
				return err
			}
			attempt, err = dispatcher.repository.BeginProductAttempt(
				ctx,
				tx,
				BeginProductAttemptCommand{
					Action:               action,
					Authorization:        authorization,
					Baseline:             baseline,
					RecheckObservationID: recheckObservationID,
				},
			)
			if err != nil {
				return err
			}
			return dispatcher.transferResults.BeginWithin(
				ctx,
				tx,
				action.TransferID,
			)
		},
	)
	beginAttemptDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	if planSuperseded {
		return ErrProductPlanSuperseded
	}
	if planStopped {
		return ErrProductPlanStopped
	}
	if authorizationExpired {
		return transfer_service.ErrLiveExpired
	}
	if attempt.ID <= 0 {
		return ErrProductActionConflict
	}

	stepStartedAt = time.Now()
	result = executeProductMutation(ctx, dispatcher.transport, snapshot)
	mutationDuration = time.Since(stepStartedAt)
	stepStartedAt = time.Now()
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	err = dispatcher.recordSubmission(resultCtx, candidate, attempt, result)
	recordResultDuration = time.Since(stepStartedAt)
	return err
}

func (dispatcher *ProductDispatcher) recordInterrupted(
	ctx context.Context,
	candidate ProductActionCandidate,
) error {
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	if err != nil {
		return err
	}
	if action.State != "dispatching" || action.AttemptID <= 0 {
		return ErrProductActionConflict
	}
	return dispatcher.recordSubmission(
		ctx,
		candidate,
		attemptFromAction(action),
		SubmissionResult{
			Delivery:          SubmissionUnknownDelivery,
			ClassifierVersion: submissionClassifierVersion,
			Disposition:       SubmissionUncertain,
			SafeCode:          "PROCESS_INTERRUPTED_AFTER_ATTEMPT_COMMIT",
			UnmatchedCount:    len(action.Members),
		},
	)
}

func (dispatcher *ProductDispatcher) recordSubmission(
	ctx context.Context,
	candidate ProductActionCandidate,
	attempt ProductAttempt,
	result SubmissionResult,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if action.State != "dispatching" || action.AttemptID != attempt.ID {
				return ErrProductActionConflict
			}
			attempt = attemptFromAction(action)
			if err := dispatcher.repository.RecordSubmission(
				ctx,
				tx,
				RecordSubmissionCommand{Action: action, Attempt: attempt, Result: result},
			); err != nil {
				return err
			}
			if result.Disposition != SubmissionRejectedProven {
				return dispatcher.transferResults.MarkReconcilingWithin(
					ctx,
					tx,
					action.TransferID,
				)
			}
			skipMediaGroups, err := dispatcher.mediaGroupsToSkip(ctx, tx, action)
			if err != nil {
				return err
			}
			memberResults := make(map[int64]MemberSubmissionResult, len(result.MemberResults))
			for _, memberResult := range result.MemberResults {
				memberResults[memberResult.ActionMemberID] = memberResult
			}
			items := make([]transfer_service.PublicationTerminalItem, 0, len(action.Members))
			for _, member := range action.Members {
				memberResult, exists := memberResults[member.ID]
				if !exists {
					return ErrProductActionConflict
				}
				items = append(items, transfer_service.PublicationTerminalItem{
					GroupTargetID:        member.GroupTargetID,
					TransferItemTargetID: member.TransferItemTargetID,
					OutcomeClass:         memberResult.OutcomeClass,
					OutcomeCode:          memberResult.OutcomeCode,
				})
			}
			if err := dispatcher.transferResults.ApplyWithin(
				ctx,
				tx,
				transfer_service.ApplyPublicationActionResultCommand{
					TransferID:         action.TransferID,
					ActionID:           action.ActionID,
					Items:              items,
					SkipMediaForGroups: skipMediaGroups,
				},
			); err != nil {
				return err
			}
			return dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, action)
		},
	)
}

func (dispatcher *ProductDispatcher) mediaGroupsToSkip(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action ProductAction,
) ([]int64, error) {
	pending, err := dispatcher.repository.PendingMediaGroupTargetIDs(ctx, tx, action)
	if err != nil {
		return nil, err
	}
	pendingSet := make(map[int64]struct{}, len(pending))
	for _, groupTargetID := range pending {
		pendingSet[groupTargetID] = struct{}{}
	}
	all := action.GroupTargetIDs()
	result := make([]int64, 0, len(all))
	for _, groupTargetID := range all {
		if _, remainsPending := pendingSet[groupTargetID]; !remainsPending {
			result = append(result, groupTargetID)
		}
	}
	return result, nil
}

func (dispatcher *ProductDispatcher) closeAuthorizationIfPlanTerminal(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action ProductAction,
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

func (dispatcher *ProductDispatcher) loadLockedAction(
	ctx context.Context,
	candidate ProductActionCandidate,
) (ProductAction, error) {
	var action ProductAction
	err := dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			action, err = dispatcher.repository.LockProductAction(ctx, tx, candidate)
			return err
		},
	)
	return action, err
}

func sameImmutableProductAction(left, right ProductAction) bool {
	if left.TransferID != right.TransferID || left.ActionID != right.ActionID ||
		left.PlanID != right.PlanID || left.TargetID != right.TargetID ||
		left.CabinetID != right.CabinetID || left.Kind != right.Kind ||
		left.PlanDigest != right.PlanDigest || left.TargetSetRoot != right.TargetSetRoot ||
		left.RequestDigest != right.RequestDigest ||
		left.MemberSetDigest != right.MemberSetDigest ||
		!bytes.Equal(left.RequestPayload, right.RequestPayload) ||
		len(left.Members) != len(right.Members) {
		return false
	}
	for index := range left.Members {
		leftMember, rightMember := left.Members[index], right.Members[index]
		if leftMember.ID != rightMember.ID ||
			leftMember.GroupTargetID != rightMember.GroupTargetID ||
			leftMember.TransferItemTargetID != rightMember.TransferItemTargetID ||
			leftMember.RequestMemberIndex != rightMember.RequestMemberIndex ||
			leftMember.VendorCode != rightMember.VendorCode {
			return false
		}
	}
	return true
}

func sameProductActionProgress(left, right ProductAction) bool {
	if !sameImmutableProductAction(left, right) {
		return false
	}
	for index := range left.Members {
		leftMember, rightMember := left.Members[index], right.Members[index]
		if leftMember.OutcomeClass != rightMember.OutcomeClass ||
			leftMember.OutcomeCode != rightMember.OutcomeCode ||
			leftMember.NMID != rightMember.NMID {
			return false
		}
	}
	return true
}

func attemptFromAction(action ProductAction) ProductAttempt {
	return ProductAttempt{
		ID:                   action.AttemptID,
		TransferID:           action.TransferID,
		ActionID:             action.ActionID,
		AuthorizationID:      action.AuthorizationID,
		RecheckObservationID: action.RecheckObservationID,
		RequestDigest:        action.RequestDigest,
		StartedAt:            action.AttemptStartedAt,
	}
}
