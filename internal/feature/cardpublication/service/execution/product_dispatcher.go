package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	cardpublication_monitoring "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"

	"go.uber.org/zap"
)

const productDispatchPageSize = 20

type ProductDispatcher struct {
	repository            ProductJournalRepository
	transport             cardpublication_catalog.CatalogTransport
	catalogReader         *cardpublication_catalog.CatalogReader
	errorFeed             *cardpublication_monitoring.ErrorFeed
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
	transport cardpublication_catalog.CatalogTransport,
	catalogReader *cardpublication_catalog.CatalogReader,
	errorFeed *cardpublication_monitoring.ErrorFeed,
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
		logger:                core_logger.Fallback(loggers...),
	}
}

func (dispatcher *ProductDispatcher) ProcessPending(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process pending product dispatch: context is nil")
	}
	dispatcher.processMu.Lock()
	defer dispatcher.processMu.Unlock()

	var firstErr error
	interrupted, err := dispatcher.repository.ListInterruptedProductActions(
		ctx,
		productDispatchPageSize,
	)
	if err != nil {
		return err
	}
	for _, candidate := range interrupted {
		if err := dispatcher.recordInterrupted(ctx, candidate); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	candidates, err := dispatcher.repository.ListDispatchableProductActions(
		ctx,
		productDispatchPageSize,
	)
	if err != nil {
		if firstErr != nil {
			return errors.Join(firstErr, err)
		}
		return err
	}
	var reconciliationErr, dispatchErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		reconciliationErr = dispatcher.reconcilePending(ctx)
	}()
	go func() {
		defer wait.Done()
		dispatchErr = dispatcher.dispatchCandidates(ctx, candidates)
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
					targetID: candidate.TargetID, sellerID: candidate.SellerID,
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
	observation cardpublication_catalog.CatalogObservation,
) error {
	var (
		planSuperseded       bool
		planStopped          bool
		authorizationExpired bool
	)
	snapshot, err := dispatcher.loadLockedAction(ctx, candidate)
	if err != nil {
		return err
	}
	recheck, err := BuildTargetedRecheck(snapshot, observation)
	if err != nil {
		return err
	}

	var attempt ProductAttempt
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
						items := make([]transfer_publication.PublicationTerminalItem, 0, len(abortedAction.Members))
						groupSet := make(map[int64]struct{})
						for _, member := range abortedAction.Members {
							items = append(items, transfer_publication.PublicationTerminalItem{
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
							transfer_publication.ApplyPublicationActionResultCommand{
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
					transfer_authorization.ChangeLiveAuthorizationStateCommand{
						TransferID:       action.TransferID,
						AuthorizationID:  action.AuthorizationID,
						ExpectedRevision: action.AuthorizationRevision,
						ExpectedPlanID:   action.PlanID,
						SafeReasonCode:   "targeted_recheck_changed",
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
				transfer_authorization.LiveAuthorizationCheck{
					TransferID:                    action.TransferID,
					ActionID:                      action.ActionID,
					AuthorizationID:               action.AuthorizationID,
					ExpectedAuthorizationRevision: action.AuthorizationRevision,
					PlanID:                        action.PlanID,
					TargetID:                      action.TargetID,
					SellerID:                      action.SellerID,
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
				cardpublication_monitoring.CaptureErrorBaselineCommand{
					TransferID: action.TransferID,
					ActionID:   action.ActionID,
					SellerID:   action.SellerID,
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

	result := executeProductMutation(ctx, dispatcher.transport, snapshot)
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	return dispatcher.recordSubmission(resultCtx, candidate, attempt, result)
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
			items := make([]transfer_publication.PublicationTerminalItem, 0, len(action.Members))
			for _, member := range action.Members {
				memberResult, exists := memberResults[member.ID]
				if !exists {
					return ErrProductActionConflict
				}
				items = append(items, transfer_publication.PublicationTerminalItem{
					GroupTargetID:        member.GroupTargetID,
					TransferItemTargetID: member.TransferItemTargetID,
					OutcomeClass:         memberResult.OutcomeClass,
					OutcomeCode:          memberResult.OutcomeCode,
				})
			}
			if err := dispatcher.transferResults.ApplyWithin(
				ctx,
				tx,
				transfer_publication.ApplyPublicationActionResultCommand{
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
	return closeTerminalPlanAuthorization(
		ctx, tx, dispatcher.repository, dispatcher.authorization,
		action.ProductActionCandidate, action.PlanID,
	)
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
		left.SellerID != right.SellerID || left.Kind != right.Kind ||
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
		StartedAt:            action.AttemptStartedAt,
	}
}
