package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const emptyResponseProductRetryDelay = 10 * time.Minute

type productCatalogKey struct {
	targetID  int64
	cabinetID CabinetID
}

type productCatalogRead struct {
	key         productCatalogKey
	vendorCodes []string
}

func (dispatcher *ProductDispatcher) reconcilePending(ctx context.Context) error {
	now := dispatcher.now().UTC()
	candidates, err := dispatcher.repository.ListReconcilingProductActions(
		ctx,
		now,
		dispatcher.reconciliationDelay,
		productDispatchPageSize,
	)
	if err != nil {
		return err
	}
	observations, catalogErr := dispatcher.readProductCatalog(ctx, candidates)
	processErr := processConcurrently(ctx, candidates, dispatcher.concurrency, func(candidate ProductActionCandidate) error {
		observation, exists := observations[productCatalogKey{
			targetID: candidate.TargetID, cabinetID: candidate.CabinetID,
		}]
		if !exists {
			return nil
		}
		if err := dispatcher.reconcile(ctx, candidate, observation, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf(
				"reconcile publication product action ID='%d': %w",
				candidate.ActionID,
				err,
			)
		}
		return nil
	})
	return errors.Join(catalogErr, processErr)
}

func (dispatcher *ProductDispatcher) readProductCatalog(
	ctx context.Context,
	candidates []ProductActionCandidate,
) (map[productCatalogKey]CatalogObservation, error) {
	vendorsByTarget := make(map[productCatalogKey]map[string]struct{})
	for _, candidate := range candidates {
		key := productCatalogKey{
			targetID: candidate.TargetID, cabinetID: candidate.CabinetID,
		}
		vendors := vendorsByTarget[key]
		if vendors == nil {
			vendors = make(map[string]struct{})
			vendorsByTarget[key] = vendors
		}
		for _, vendorCode := range candidate.CatalogVendorCodes {
			vendors[vendorCode] = struct{}{}
		}
	}
	reads := make([]productCatalogRead, 0, len(vendorsByTarget))
	for key, vendors := range vendorsByTarget {
		vendorCodes := make([]string, 0, len(vendors))
		for vendorCode := range vendors {
			vendorCodes = append(vendorCodes, vendorCode)
		}
		sort.Strings(vendorCodes)
		reads = append(reads, productCatalogRead{key: key, vendorCodes: vendorCodes})
	}
	sort.Slice(reads, func(left, right int) bool {
		if reads[left].key.cabinetID != reads[right].key.cabinetID {
			return reads[left].key.cabinetID < reads[right].key.cabinetID
		}
		return reads[left].key.targetID < reads[right].key.targetID
	})

	loaded := make([]CatalogObservation, len(reads))
	readErrors := make([]error, len(reads))
	var group errgroup.Group
	group.SetLimit(dispatcher.concurrency)
	for index, read := range reads {
		index, read := index, read
		group.Go(func() error {
			loaded[index], readErrors[index] = dispatcher.catalogReader.ReadVendorCodes(
				ctx,
				read.key.targetID,
				read.key.cabinetID,
				read.vendorCodes,
			)
			return nil
		})
	}
	_ = group.Wait()
	result := make(map[productCatalogKey]CatalogObservation, len(reads))
	for index, read := range reads {
		if readErrors[index] == nil {
			result[read.key] = loaded[index]
		}
	}
	return result, errors.Join(readErrors...)
}

func (dispatcher *ProductDispatcher) reconcile(
	ctx context.Context,
	candidate ProductActionCandidate,
	observation CatalogObservation,
	now time.Time,
) (err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(candidate.TransferID), ActionID: candidate.ActionID,
	})
	core_observability.LogStarted(dispatcher.logger, "cardpublication", "reconcile_product_action",
		zap.Int64("transfer_id", int64(candidate.TransferID)), zap.Int64("action_id", candidate.ActionID),
		zap.String("cabinet_id", string(candidate.CabinetID)))
	var (
		loadActionDuration   time.Duration
		buildDuration        time.Duration
		loadErrorsDuration   time.Duration
		classifyDuration     time.Duration
		persistDuration      time.Duration
		attemptAge           time.Duration
		membersCount         int
		pendingMembersCount  int
		resolvedMembersCount int
		matchesCount         int
		complete             bool
		actionClass          transfer_service.ResultClass
		actionCode           string
	)
	defer func() {
		core_observability.LogTiming(
			dispatcher.logger,
			"cardpublication",
			"reconcile_product_action",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(candidate.TransferID)),
			zap.Int64("action_id", candidate.ActionID),
			zap.Int64("target_id", candidate.TargetID),
			zap.String("cabinet_id", string(candidate.CabinetID)),
			zap.Int("members_count", membersCount),
			zap.Int("pending_members_count", pendingMembersCount),
			zap.Int("resolved_members_count", resolvedMembersCount),
			zap.Int("error_matches_count", matchesCount),
			zap.Bool("complete", complete),
			zap.Bool("catalog_batch_read", true),
			zap.Duration("attempt_age", attemptAge),
			zap.Duration("reconciliation_delay", dispatcher.reconciliationDelay),
			zap.Duration("reconciliation_timeout", dispatcher.reconciliationTimeout),
			zap.String("outcome_class", string(actionClass)),
			zap.String("outcome_code", actionCode),
			zap.Duration("load_action_duration", loadActionDuration),
			zap.Duration("build_evidence_duration", buildDuration),
			zap.Duration("load_error_matches_duration", loadErrorsDuration),
			zap.Duration("classify_duration", classifyDuration),
			zap.Duration("persist_duration", persistDuration),
		)
	}()
	stepStartedAt := time.Now()
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	loadActionDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	if action.State != "reconciling" || action.AttemptID <= 0 ||
		action.AttemptStartedAt.IsZero() {
		return ErrProductActionConflict
	}
	if action.EmptyResponseRetry.ID > 0 && action.EmptyResponseRetry.FinishedAt.IsZero() {
		if err := dispatcher.recordProductRetry(
			ctx,
			candidate,
			action.EmptyResponseRetry,
			SubmissionResult{
				Delivery:          SubmissionUnknownDelivery,
				ClassifierVersion: submissionClassifierVersion,
				Disposition:       SubmissionUncertain,
				SafeCode:          "EMPTY_RESPONSE_RETRY_INTERRUPTED",
				UnmatchedCount:    len(action.PendingMembers()),
			},
		); err != nil {
			return err
		}
		action, err = dispatcher.loadLockedAction(ctx, candidate)
		if err != nil {
			return err
		}
	}
	membersCount = len(action.Members)
	pendingMembersCount = len(action.PendingMembers())
	effectiveAttemptStartedAt := action.AttemptStartedAt
	if action.EmptyResponseRetry.ID > 0 {
		effectiveAttemptStartedAt = action.EmptyResponseRetry.StartedAt
	}
	attemptAge = now.Sub(effectiveAttemptStartedAt)
	stepStartedAt = time.Now()
	targeted, err := BuildTargetedRecheck(action, observation)
	buildDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	stepStartedAt = time.Now()
	matches, err := dispatcher.repository.LoadErrorBatchMatches(ctx, action)
	loadErrorsDuration = time.Since(stepStartedAt)
	if err != nil {
		return err
	}
	matchesCount = len(matches)
	if shouldRetryEmptyProductResponse(
		action,
		observation,
		matches,
		now,
		emptyResponseProductRetryDelay,
	) {
		stepStartedAt = time.Now()
		err = dispatcher.retryAfterEmptyResponse(ctx, candidate, action, targeted)
		persistDuration = time.Since(stepStartedAt)
		return err
	}
	stepStartedAt = time.Now()
	var memberResults []MemberReconciliationResult
	var completeResult bool
	var resultClass transfer_service.ResultClass
	var resultCode string
	if retryResponseProvedRejection(action.EmptyResponseRetry) {
		memberResults = rejectedRetryResults(action)
		completeResult = true
		resultClass = transfer_service.ResultRejected
		resultCode = "PRODUCT_RETRY_REJECTED_BY_WB"
	} else {
		deadlineAction := action
		deadlineAction.AttemptStartedAt = effectiveAttemptStartedAt
		memberResults, completeResult, resultClass, resultCode = reconcileProductEvidence(
			deadlineAction,
			observation,
			matches,
			now,
			dispatcher.reconciliationTimeout,
		)
	}
	resolvedMembersCount = len(memberResults)
	classifyDuration = time.Since(stepStartedAt)
	complete = completeResult
	actionClass = resultClass
	actionCode = resultCode
	stepStartedAt = time.Now()
	if !complete {
		err = dispatcher.recordIncompleteReconciliation(
			ctx,
			candidate,
			action,
			targeted,
			memberResults,
		)
		persistDuration = time.Since(stepStartedAt)
		return err
	}

	err = dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			locked, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if locked.State != "reconciling" || !sameProductActionProgress(action, locked) ||
				locked.AttemptID != action.AttemptID {
				return ErrProductActionConflict
			}
			postObservationID, err := dispatcher.repository.InsertActionObservation(
				ctx,
				tx,
				locked.TransferID,
				"post_submission",
				targeted.Observation,
			)
			if err != nil {
				return err
			}
			if err := dispatcher.repository.FinishProductReconciliation(
				ctx,
				tx,
				locked,
				postObservationID,
				memberResults,
				actionClass,
				actionCode,
			); err != nil {
				return err
			}
			skipMediaGroups, err := dispatcher.mediaGroupsToSkip(ctx, tx, locked)
			if err != nil {
				return err
			}
			items := make([]transfer_service.PublicationTerminalItem, 0, len(locked.Members))
			byMember := make(map[int64]MemberReconciliationResult, len(memberResults))
			for _, result := range memberResults {
				byMember[result.ActionMemberID] = result
			}
			for _, member := range locked.Members {
				result, exists := byMember[member.ID]
				if !exists && member.OutcomeClass != "" {
					result = MemberReconciliationResult{
						ActionMemberID: member.ID,
						OutcomeClass:   member.OutcomeClass,
						OutcomeCode:    member.OutcomeCode,
						NMID:           member.NMID,
					}
					exists = true
				}
				if !exists {
					return ErrProductActionConflict
				}
				items = append(items, transfer_service.PublicationTerminalItem{
					GroupTargetID:        member.GroupTargetID,
					TransferItemTargetID: member.TransferItemTargetID,
					OutcomeClass:         result.OutcomeClass,
					OutcomeCode:          result.OutcomeCode,
					NMID:                 result.NMID,
				})
			}
			if err := dispatcher.transferResults.ApplyWithin(
				ctx,
				tx,
				transfer_service.ApplyPublicationActionResultCommand{
					TransferID:         locked.TransferID,
					ActionID:           locked.ActionID,
					Items:              items,
					SkipMediaForGroups: skipMediaGroups,
				},
			); err != nil {
				return err
			}
			return dispatcher.closeAuthorizationIfPlanTerminal(ctx, tx, locked)
		},
	)
	persistDuration = time.Since(stepStartedAt)
	return err
}

func shouldRetryEmptyProductResponse(
	action ProductAction,
	observation CatalogObservation,
	matches []ErrorBatchMatch,
	now time.Time,
	delay time.Duration,
) bool {
	if delay <= 0 || action.EmptyResponseRetry.ID != 0 ||
		action.AttemptDelivery != SubmissionResponseReceived ||
		action.AttemptHTTPStatus != 200 ||
		action.AttemptFinishedAt.IsZero() ||
		now.Before(action.AttemptFinishedAt.Add(delay)) ||
		len(action.PendingMembers()) != len(action.Members) || len(matches) != 0 {
		return false
	}
	if action.AttemptSafeCode != "WB_TRANSPORT_EMPTY_RESPONSE" &&
		action.AttemptSafeCode != "WB_TRANSPORT_INVALID_RESPONSE" {
		return false
	}
	return len(observation.Normal) == 0 && len(observation.Trash) == 0
}

func retryResponseProvedRejection(retry ProductRetry) bool {
	return retry.ID > 0 && !retry.FinishedAt.IsZero() &&
		retry.Delivery == SubmissionResponseReceived &&
		retry.Disposition == SubmissionRejectedProven
}

func rejectedRetryResults(action ProductAction) []MemberReconciliationResult {
	results := make([]MemberReconciliationResult, 0, len(action.Members))
	for _, member := range action.Members {
		if member.OutcomeClass == "" {
			results = append(results, MemberReconciliationResult{
				ActionMemberID: member.ID,
				OutcomeClass:   transfer_service.ResultRejected,
				OutcomeCode:    "WB_RETRY_ENVELOPE_MEMBER_REJECTED",
			})
		}
	}
	return results
}

func (dispatcher *ProductDispatcher) retryAfterEmptyResponse(
	ctx context.Context,
	candidate ProductActionCandidate,
	snapshot ProductAction,
	targeted TargetedRecheck,
) (err error) {
	startedAt := time.Now()
	var retry ProductRetry
	var result SubmissionResult
	defer func() {
		core_observability.LogTiming(
			dispatcher.logger,
			"cardpublication",
			"retry_product_after_empty_response",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(candidate.TransferID)),
			zap.Int64("action_id", candidate.ActionID),
			zap.String("cabinet_id", string(candidate.CabinetID)),
			zap.Int64("retry_id", retry.ID),
			zap.String("delivery", string(result.Delivery)),
			zap.String("disposition", string(result.Disposition)),
			zap.String("outcome_code", result.SafeCode),
		)
	}()
	err = dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if !sameProductActionProgress(snapshot, action) ||
				action.EmptyResponseRetry.ID != 0 {
				return ErrProductActionConflict
			}
			observationID, err := dispatcher.repository.InsertActionObservation(
				ctx,
				tx,
				action.TransferID,
				"post_submission",
				targeted.Observation,
			)
			if err != nil {
				return err
			}
			if err := dispatcher.repository.RecordProductReconciliationPoll(
				ctx,
				tx,
				action,
				observationID,
				nil,
			); err != nil {
				return err
			}
			retry, err = dispatcher.repository.BeginProductRetry(
				ctx,
				tx,
				BeginProductRetryCommand{Action: action},
			)
			return err
		},
	)
	if err != nil {
		return err
	}

	result = executeProductMutation(ctx, dispatcher.transport, snapshot)
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	return dispatcher.recordProductRetry(resultCtx, candidate, retry, result)
}

func (dispatcher *ProductDispatcher) recordProductRetry(
	ctx context.Context,
	candidate ProductActionCandidate,
	retry ProductRetry,
	result SubmissionResult,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			action, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if action.State != "reconciling" ||
				action.EmptyResponseRetry.ID != retry.ID ||
				!action.EmptyResponseRetry.FinishedAt.IsZero() {
				return ErrProductActionConflict
			}
			return dispatcher.repository.RecordProductRetry(
				ctx,
				tx,
				RecordProductRetryCommand{
					Action: action,
					Retry:  action.EmptyResponseRetry,
					Result: result,
				},
			)
		},
	)
}

func (dispatcher *ProductDispatcher) recordIncompleteReconciliation(
	ctx context.Context,
	candidate ProductActionCandidate,
	action ProductAction,
	targeted TargetedRecheck,
	memberResults []MemberReconciliationResult,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			locked, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if locked.State != "reconciling" || !sameProductActionProgress(action, locked) ||
				locked.AttemptID != action.AttemptID {
				return ErrProductActionConflict
			}
			postObservationID, err := dispatcher.repository.InsertActionObservation(
				ctx,
				tx,
				locked.TransferID,
				"post_submission",
				targeted.Observation,
			)
			if err != nil {
				return err
			}
			return dispatcher.repository.RecordProductReconciliationPoll(
				ctx,
				tx,
				locked,
				postObservationID,
				memberResults,
			)
		},
	)
}

func reconcileProductEvidence(
	action ProductAction,
	observation CatalogObservation,
	matches []ErrorBatchMatch,
	now time.Time,
	timeout time.Duration,
) ([]MemberReconciliationResult, bool, transfer_service.ResultClass, string) {
	normalByVendor := make(map[string][]contentapi.Card)
	for _, card := range observation.Normal {
		normalByVendor[card.VendorCode] = append(normalByVendor[card.VendorCode], card)
	}
	trashByVendor := make(map[string][]contentapi.TrashCard)
	for _, card := range observation.Trash {
		trashByVendor[card.VendorCode] = append(trashByVendor[card.VendorCode], card)
	}
	errorsByVendor := make(map[string][]ErrorBatchMatch)
	for _, match := range matches {
		for _, vendorCode := range match.RejectedVendorCodes {
			errorsByVendor[vendorCode] = append(errorsByVendor[vendorCode], match)
		}
	}
	deadlineReached := !action.AttemptStartedAt.IsZero() &&
		!now.Before(action.AttemptStartedAt.Add(timeout))
	results := make([]MemberReconciliationResult, 0, len(action.Members))
	unresolved, rejected, succeeded := 0, 0, 0
	pending := 0
	for _, member := range action.Members {
		if member.OutcomeClass != "" {
			switch member.OutcomeClass {
			case transfer_service.ResultSuccess, transfer_service.ResultSkipped:
				succeeded++
			case transfer_service.ResultRejected:
				rejected++
			default:
				unresolved++
			}
			continue
		}
		normal := normalByVendor[member.VendorCode]
		trash := trashByVendor[member.VendorCode]
		errorMatches := errorsByVendor[member.VendorCode]
		result := MemberReconciliationResult{ActionMemberID: member.ID}
		switch {
		case len(normal) == 1 && len(trash) == 0 && len(errorMatches) == 0:
			card := normal[0]
			if !observedCardMatchesProductAction(action, member, card) {
				result.OutcomeClass = transfer_service.ResultUnresolved
				result.OutcomeCode = "REMOTE_PRODUCT_BINDING_MISMATCH"
				unresolved++
				break
			}
			result.OutcomeClass = transfer_service.ResultSuccess
			result.OutcomeCode = "CREATED_CONFIRMED_BY_VENDOR_CODE"
			result.NMID = card.NMID
			result.IMTID = card.IMTID
			result.SubjectID = card.SubjectID
			result.AttributionLevel = "observed_after_attempt"
			succeeded++
		case len(normal) == 0 && len(trash) == 0 && len(errorMatches) == 1:
			result.OutcomeClass = transfer_service.ResultRejected
			result.OutcomeCode = "WB_ERROR_LIST_REJECTED"
			result.ErrorBatchID = errorMatches[0].ID
			rejected++
		case len(normal) > 0 || len(trash) > 0 || len(errorMatches) > 1:
			result.OutcomeClass = transfer_service.ResultUnresolved
			result.OutcomeCode = "REMOTE_EVIDENCE_CONFLICT"
			unresolved++
		case deadlineReached:
			result.OutcomeClass = transfer_service.ResultUnresolved
			result.OutcomeCode = "RECONCILIATION_TIMEOUT"
			unresolved++
		default:
			pending++
			continue
		}
		results = append(results, result)
	}
	if pending > 0 {
		return results, false, "", ""
	}
	if unresolved > 0 {
		return results, true, transfer_service.ResultUnresolved, "PRODUCT_RECONCILIATION_UNRESOLVED"
	}
	if rejected == len(action.Members) {
		return results, true, transfer_service.ResultRejected, "PRODUCT_REJECTED_BY_ERROR_LIST"
	}
	if succeeded == len(action.Members) {
		return results, true, transfer_service.ResultSuccess, "PRODUCT_CREATED_BY_VENDOR_CODE"
	}
	return results, true, transfer_service.ResultPartial, "PRODUCT_RECONCILIATION_PARTIAL"
}

func observedCardMatchesProductAction(
	action ProductAction,
	member ProductActionMember,
	card contentapi.Card,
) bool {
	if card.NMID <= 0 || card.IMTID <= 0 || card.SubjectID <= 0 ||
		card.VendorCode != member.VendorCode {
		return false
	}
	switch action.Kind {
	case ActionCreateGroup:
		var request contentapi.UploadCardsRequest
		if decodeExactJSON(action.RequestPayload, &request) != nil {
			return false
		}
		requestIndex := 0
		for _, group := range request {
			for _, variant := range group.Variants {
				if requestIndex == member.RequestMemberIndex {
					return variant.VendorCode == member.VendorCode &&
						group.SubjectID == card.SubjectID
				}
				requestIndex++
			}
		}
	case ActionAddToGroup:
		var request contentapi.UploadCardsAddRequest
		if decodeExactJSON(action.RequestPayload, &request) != nil ||
			member.RequestMemberIndex < 0 ||
			member.RequestMemberIndex >= len(request.CardsToAdd) {
			return false
		}
		return request.IMTID == card.IMTID &&
			request.CardsToAdd[member.RequestMemberIndex].VendorCode == member.VendorCode
	}
	return false
}

func ValidateMemberReconciliationResults(
	action ProductAction,
	results []MemberReconciliationResult,
) error {
	pending := make(map[int64]struct{}, len(action.Members))
	for _, member := range action.Members {
		if member.OutcomeClass == "" {
			pending[member.ID] = struct{}{}
		}
	}
	seen := make(map[int64]struct{}, len(results))
	for _, result := range results {
		if result.ActionMemberID <= 0 || !result.OutcomeClass.IsValid() ||
			result.OutcomeCode == "" || len(result.OutcomeCode) > 128 ||
			result.NMID < 0 || result.IMTID < 0 || result.SubjectID < 0 ||
			result.ErrorBatchID < 0 {
			return errors.New("member reconciliation result is invalid")
		}
		if _, exists := seen[result.ActionMemberID]; exists {
			return ErrProductActionConflict
		}
		if _, exists := pending[result.ActionMemberID]; !exists {
			return ErrProductActionConflict
		}
		if result.AttributionLevel != "" &&
			result.AttributionLevel != "observed_after_attempt" {
			return errors.New("member reconciliation attribution level is invalid")
		}
		if result.OutcomeClass == transfer_service.ResultSuccess {
			if result.AttributionLevel != "observed_after_attempt" || result.NMID <= 0 ||
				result.IMTID <= 0 || result.SubjectID <= 0 || result.ErrorBatchID != 0 {
				return errors.New("successful member reconciliation attribution is invalid")
			}
		} else if result.AttributionLevel != "" {
			return errors.New("non-successful member reconciliation has attribution")
		}
		seen[result.ActionMemberID] = struct{}{}
	}
	return nil
}
