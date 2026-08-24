package cardpublication_service

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (dispatcher *ProductDispatcher) reconcilePending(ctx context.Context) error {
	now := dispatcher.now().UTC()
	candidates, err := dispatcher.repository.ListReconcilingProductActions(
		ctx,
		now.Add(-dispatcher.reconciliationDelay),
		productDispatchPageSize,
	)
	if err != nil {
		return err
	}
	var firstErr error
	for _, candidate := range candidates {
		if err := dispatcher.reconcile(ctx, candidate, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"reconcile publication product action ID='%d': %w",
					candidate.ActionID,
					err,
				)
			}
		}
	}
	return firstErr
}

func (dispatcher *ProductDispatcher) reconcile(
	ctx context.Context,
	candidate ProductActionCandidate,
	now time.Time,
) error {
	action, err := dispatcher.loadLockedAction(ctx, candidate)
	if err != nil {
		return err
	}
	if action.State != "reconciling" || action.AttemptID <= 0 ||
		action.AttemptStartedAt.IsZero() {
		return ErrProductActionConflict
	}
	observation, err := dispatcher.catalogReader.Read(
		ctx,
		action.TargetID,
		action.CabinetID,
	)
	if err != nil {
		return err
	}
	targeted, err := BuildTargetedRecheck(action, observation)
	if err != nil {
		return err
	}
	matches, err := dispatcher.repository.LoadErrorBatchMatches(ctx, action)
	if err != nil {
		return err
	}
	memberResults, complete, actionClass, actionCode := reconcileProductEvidence(
		action,
		observation,
		matches,
		now,
		dispatcher.reconciliationTimeout,
	)
	if !complete {
		return dispatcher.recordIncompleteReconciliation(ctx, candidate, action, targeted)
	}

	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			locked, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if locked.State != "reconciling" || !sameImmutableProductAction(action, locked) ||
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
			items := make([]transfer_service.PublicationTerminalItem, 0, len(memberResults))
			byMember := make(map[int64]MemberReconciliationResult, len(memberResults))
			for _, result := range memberResults {
				byMember[result.ActionMemberID] = result
			}
			for _, member := range locked.Members {
				result, exists := byMember[member.ID]
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
}

func (dispatcher *ProductDispatcher) recordIncompleteReconciliation(
	ctx context.Context,
	candidate ProductActionCandidate,
	action ProductAction,
	targeted TargetedRecheck,
) error {
	return dispatcher.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			locked, err := dispatcher.repository.LockProductAction(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if locked.State != "reconciling" || !sameImmutableProductAction(action, locked) ||
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
	for _, member := range action.Members {
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
			return nil, false, "", ""
		}
		results = append(results, result)
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
	if len(results) != len(action.Members) {
		return ErrProductActionConflict
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
