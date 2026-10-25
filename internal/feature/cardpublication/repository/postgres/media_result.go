package cardpublication_postgres_repository

import (
	"context"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

// RecordMediaResult записывает результат выполнения попытки медиа-действия и завершает действие.
func (repository *Repository) RecordMediaResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.MediaAction,
	attempt cardpublication_execution.MediaAttempt,
	mediaResult cardpublication_execution.MediaMutationResult,
) (cardpublication_execution.MediaGroupResult, error) {
	if err := recordMediaAttemptResult(ctx, tx, action, attempt, mediaResult); err != nil {
		return cardpublication_execution.MediaGroupResult{}, err
	}
	return finishMediaAction(ctx, tx, action, mediaResult.OutcomeClass, mediaResult.OutcomeCode)
}

// recordMediaAttemptResult фиксирует исход попытки в wb.publication_attempts.
func recordMediaAttemptResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.MediaAction,
	attempt cardpublication_execution.MediaAttempt,
	mediaResult cardpublication_execution.MediaMutationResult,
) error {
	if tx == nil || action.State != "dispatching" || action.AttemptID != attempt.ID ||
		attempt.ActionID != action.ActionID || attempt.TransferID != action.TransferID ||
		attempt.AuthorizationID != action.AuthorizationID ||
		attempt.AttributionID != action.AttributionID ||
		!equalBytes(attempt.RequestPayload, action.AttemptRequestPayload) {
		return cardpublication_execution.ErrMediaActionConflict
	}
	if err := mediaResult.Validate(); err != nil {
		return err
	}
	const lockAttempt = `
		SELECT request_payload, COALESCE(attribution_id, 0)
		FROM wb.publication_attempts
		WHERE transfer_id = $1
			AND id = $2
			AND action_id = $3
			AND authorization_id = $4
			AND finished_at IS NULL
		FOR UPDATE;
	`
	var storedPayload []byte
	var storedAttributionID int64
	if err := tx.QueryRow(
		ctx,
		lockAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
	).Scan(&storedPayload, &storedAttributionID); err != nil {
		return fmt.Errorf("lock publication media attempt result: %w", err)
	}
	if !equalBytes(storedPayload, attempt.RequestPayload) ||
		storedAttributionID != action.AttributionID {
		return cardpublication_execution.ErrMediaActionConflict
	}
	var httpStatus any
	if mediaResult.HTTPStatus > 0 {
		httpStatus = mediaResult.HTTPStatus
	}
	const finishAttempt = `
		UPDATE wb.publication_attempts
		SET delivery_state = $5,
		    http_status = $6,
		    response_disposition = $7,
		    classifier_version = $8,
		    safe_error_code = $9,
		    unmatched_count = $10,
		    finished_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND action_id = $3
			AND authorization_id = $4
			AND finished_at IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		finishAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
		mediaResult.Delivery,
		httpStatus,
		mediaResult.Disposition,
		mediaResult.ClassifierVersion,
		mediaResult.OutcomeCode,
		mediaResult.UnmatchedCount,
	)
	if err != nil {
		return fmt.Errorf("finish publication media attempt: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.ErrMediaActionConflict
	}
	return nil
}

// finishMediaAction переводит медиа-действие и его элементы в терминальное состояние и освобождает блокировку товара.
func finishMediaAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.MediaAction,
	outcomeClass transfer_model.ResultClass,
	outcomeCode string,
) (cardpublication_execution.MediaGroupResult, error) {
	const finishMember = `
		UPDATE wb.publication_action_members
		SET outcome_class = $4,
		    outcome_code = $5,
		    nm_id = $6,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND action_id = $2
			AND id = $3
			AND outcome_class IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		finishMember,
		action.TransferID,
		action.ActionID,
		action.Member.ID,
		outcomeClass,
		outcomeCode,
		action.NMID,
	)
	if err != nil {
		return cardpublication_execution.MediaGroupResult{},
			fmt.Errorf("finish publication media member: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.MediaGroupResult{},
			cardpublication_execution.ErrMediaActionConflict
	}
	const finishAction = `
		UPDATE wb.publication_actions
		SET state = 'terminal',
		    outcome_class = $4,
		    outcome_code = $5,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND authorization_id = $3
			AND kind = 'upload_media'
			AND state = $6;
	`
	result, err = tx.Exec(
		ctx,
		finishAction,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		outcomeClass,
		outcomeCode,
		action.State,
	)
	if err != nil {
		return cardpublication_execution.MediaGroupResult{},
			fmt.Errorf("finish publication media action: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.MediaGroupResult{},
			cardpublication_execution.ErrMediaActionConflict
	}
	const releaseIdentity = `
		UPDATE wb.product_identities
		SET active_transfer_id = NULL,
		    active_action_id = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE seller_id = $1
			AND vendor_code_key = $2
			AND state = 'remote_present'
			AND nm_id = $5
			AND active_transfer_id = $3
			AND active_action_id = $4;
	`
	result, err = tx.Exec(
		ctx,
		releaseIdentity,
		action.SellerID,
		action.Member.VendorCode,
		action.TransferID,
		action.ActionID,
		action.NMID,
	)
	if err != nil {
		return cardpublication_execution.MediaGroupResult{},
			fmt.Errorf("release publication media identity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.MediaGroupResult{},
			cardpublication_execution.ErrMediaActionConflict
	}
	if err := reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID); err != nil {
		return cardpublication_execution.MediaGroupResult{}, err
	}
	return loadMediaGroupResult(ctx, tx, action)
}

// loadMediaGroupResult загружает агрегированный статус группы медиа-действий.
func loadMediaGroupResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.MediaAction,
) (cardpublication_execution.MediaGroupResult, error) {
	const query = `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE media_action.state IN ('terminal', 'superseded')) AS terminal,
			COUNT(*) FILTER (WHERE media_action.outcome_class = 'rejected') AS rejected,
			COUNT(*) FILTER (WHERE media_action.outcome_class = 'unresolved') AS unresolved,
			COUNT(*) FILTER (WHERE media_action.outcome_class = 'internal_error') AS internal_error,
			COUNT(*) FILTER (WHERE media_action.outcome_class = 'success') AS succeeded,
			COUNT(*) FILTER (WHERE media_action.outcome_class = 'skipped') AS skipped
		FROM wb.publication_actions AS media_action
		JOIN wb.publication_action_members AS member
		  ON member.transfer_id = media_action.transfer_id
		 AND member.action_id = media_action.id
		WHERE media_action.transfer_id = $1
			AND media_action.plan_id = $2
			AND media_action.kind = 'upload_media'
			AND member.group_target_id = $3;
	`
	var total, terminal, rejected, unresolved, internal, succeeded, skipped int64
	if err := tx.QueryRow(
		ctx,
		query,
		action.TransferID,
		action.PlanID,
		action.Member.GroupTargetID,
	).Scan(
		&total,
		&terminal,
		&rejected,
		&unresolved,
		&internal,
		&succeeded,
		&skipped,
	); err != nil {
		return cardpublication_execution.MediaGroupResult{},
			fmt.Errorf("load publication media group result: %w", err)
	}
	result := cardpublication_execution.MediaGroupResult{
		GroupTargetID: action.Member.GroupTargetID,
		Terminal:      total > 0 && terminal == total,
	}
	if !result.Terminal {
		return result, nil
	}
	switch {
	case unresolved > 0 || internal > 0:
		result.OutcomeClass = transfer_service.ResultUnresolved
		result.OutcomeCode = "MEDIA_REQUIRES_ATTENTION"
	case rejected > 0:
		result.OutcomeClass = transfer_service.ResultRejected
		result.OutcomeCode = "MEDIA_REJECTED"
	case succeeded > 0:
		result.OutcomeClass = transfer_service.ResultSuccess
		result.OutcomeCode = "MEDIA_REQUEST_ACCEPTED"
	case skipped == total:
		result.OutcomeClass = transfer_service.ResultSkipped
		result.OutcomeCode = "MEDIA_SKIPPED"
	default:
		return cardpublication_execution.MediaGroupResult{},
			cardpublication_execution.ErrMediaActionConflict
	}
	if err := result.Validate(); err != nil {
		return cardpublication_execution.MediaGroupResult{}, err
	}
	return result, nil
}

var _ cardpublication_execution.MediaJournalRepository = (*Repository)(nil)
