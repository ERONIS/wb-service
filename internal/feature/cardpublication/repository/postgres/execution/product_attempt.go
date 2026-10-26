package execution

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

// BeginProductAttempt начинает попытку публикации карточек товаров, связывая идентичности в mutation_pending.
func (repository *ExecutionRepository) BeginProductAttempt(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_execution.BeginProductAttemptCommand,
) (cardpublication_execution.ProductAttempt, error) {
	action := command.Action
	if tx == nil || action.State != "planned" || action.AttemptID != 0 ||
		command.Authorization.AuthorizationID != action.AuthorizationID ||
		command.Authorization.Revision != action.AuthorizationRevision ||
		command.Baseline.Validate() != nil ||
		command.Baseline.TransferID != action.TransferID ||
		command.Baseline.ActionID != action.ActionID ||
		command.Baseline.SellerID != action.SellerID ||
		command.RecheckObservationID <= 0 {
		return cardpublication_execution.ProductAttempt{}, errors.New(
			"begin publication product attempt command is invalid",
		)
	}
	const bindIdentities = `
		UPDATE wb.product_identities
		SET state = 'mutation_pending',
		    active_transfer_id = $3,
		    active_action_id = $4,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE seller_id = $1
			AND vendor_code_key = ANY($2::text[])
			AND state = 'remote_missing'
			AND active_action_id IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		bindIdentities,
		action.SellerID,
		action.VendorCodes(),
		action.TransferID,
		action.ActionID,
	)
	if err != nil {
		return cardpublication_execution.ProductAttempt{}, fmt.Errorf(
			"bind publication product identities: %w",
			err,
		)
	}
	if result.RowsAffected() != int64(len(action.Members)) {
		return cardpublication_execution.ProductAttempt{}, cardpublication_execution.ErrProductActionConflict
	}
	const beginAction = `
		UPDATE wb.publication_actions AS action
		SET state = 'dispatching',
		    authorization_id = $4,
		    started_at = CURRENT_TIMESTAMP,
		    revision = action.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.revision = $3
			AND action.state = 'planned'
			AND action.authorization_id IS NULL;
	`
	result, err = tx.Exec(
		ctx,
		beginAction,
		action.TransferID,
		action.ActionID,
		action.Revision,
		action.AuthorizationID,
	)
	if err != nil {
		return cardpublication_execution.ProductAttempt{}, fmt.Errorf(
			"begin publication product action: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.ProductAttempt{}, cardpublication_execution.ErrProductActionConflict
	}
	const beginPlan = `
		UPDATE wb.publication_plans
		SET state = 'executing',
		    started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND state IN ('awaiting_authorization', 'executing');
	`
	result, err = tx.Exec(ctx, beginPlan, action.TransferID, action.PlanID)
	if err != nil {
		return cardpublication_execution.ProductAttempt{}, fmt.Errorf(
			"begin publication plan execution: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.ProductAttempt{}, cardpublication_execution.ErrProductActionConflict
	}
	const insertAttempt = `
		INSERT INTO wb.publication_attempts (
			transfer_id,
			action_id,
			authorization_id,
			recheck_observation_id,
			baseline_seller_id,
			baseline_cursor_revision,
			baseline_cursor_updated_at,
			baseline_cursor_batch_uuid,
			baseline_captured_at,
			request_payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, started_at;
	`
	attempt := cardpublication_execution.ProductAttempt{
		TransferID:           action.TransferID,
		ActionID:             action.ActionID,
		AuthorizationID:      action.AuthorizationID,
		RecheckObservationID: command.RecheckObservationID,
	}
	if err := tx.QueryRow(
		ctx,
		insertAttempt,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		command.RecheckObservationID,
		command.Baseline.SellerID,
		command.Baseline.CursorRevision,
		nullableTime(command.Baseline.CursorUpdatedAt),
		command.Baseline.CursorBatchUUID,
		command.Baseline.CapturedAt,
		action.RequestPayload,
	).Scan(&attempt.ID, &attempt.StartedAt); err != nil {
		return cardpublication_execution.ProductAttempt{}, fmt.Errorf(
			"insert publication product attempt: %w",
			err,
		)
	}
	return attempt, nil
}

// RecordSubmission фиксирует ответ Wildberries на отправку пакета создания/добавления карточек.
func (repository *ExecutionRepository) RecordSubmission(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_execution.RecordSubmissionCommand,
) error {
	if tx == nil {
		return errors.New("record product submission: DBTX is nil")
	}
	if err := cardpublication_execution.ValidateSubmissionResult(command.Result); err != nil {
		return err
	}
	action, attempt, result := command.Action, command.Attempt, command.Result
	if action.State != "dispatching" || action.AttemptID != attempt.ID ||
		attempt.ActionID != action.ActionID || attempt.TransferID != action.TransferID ||
		attempt.AuthorizationID != action.AuthorizationID {
		return cardpublication_execution.ErrProductActionConflict
	}
	const lockAttempt = `
		SELECT request_payload
		FROM wb.publication_attempts
		WHERE transfer_id = $1
			AND id = $2
			AND action_id = $3
			AND authorization_id = $4
			AND finished_at IS NULL
		FOR UPDATE;
	`
	var storedPayload []byte
	if err := tx.QueryRow(
		ctx,
		lockAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
	).Scan(&storedPayload); err != nil {
		return fmt.Errorf("lock publication product attempt result: %w", err)
	}
	if !equalBytes(storedPayload, action.RequestPayload) {
		return cardpublication_execution.ErrProductActionConflict
	}
	var httpStatus any
	if result.HTTPStatus > 0 {
		httpStatus = result.HTTPStatus
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
	update, err := tx.Exec(
		ctx,
		finishAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
		result.Delivery,
		httpStatus,
		result.Disposition,
		result.ClassifierVersion,
		result.SafeCode,
		result.UnmatchedCount,
	)
	if err != nil {
		return fmt.Errorf("finish publication product attempt: %w", err)
	}
	if update.RowsAffected() != 1 {
		return cardpublication_execution.ErrProductActionConflict
	}

	if result.Disposition == cardpublication_execution.SubmissionAccepted ||
		result.Disposition == cardpublication_execution.SubmissionUncertain {
		const reconcileAction = `
			UPDATE wb.publication_actions
			SET state = 'reconciling',
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND id = $2
				AND state = 'dispatching'
				AND authorization_id = $3;
		`
		update, err := tx.Exec(
			ctx,
			reconcileAction,
			action.TransferID,
			action.ActionID,
			action.AuthorizationID,
		)
		if err != nil {
			return fmt.Errorf("reconcile publication product action: %w", err)
		}
		if update.RowsAffected() != 1 {
			return cardpublication_execution.ErrProductActionConflict
		}
		if result.Disposition == cardpublication_execution.SubmissionUncertain {
			const blockIdentities = `
				UPDATE wb.product_identities
				SET state = 'blocked_uncertain',
				    revision = revision + 1,
				    updated_at = CURRENT_TIMESTAMP
				WHERE seller_id = $1
					AND vendor_code_key = ANY($2::text[])
					AND active_transfer_id = $3
					AND active_action_id = $4
					AND state = 'mutation_pending';
			`
			if _, err := tx.Exec(
				ctx,
				blockIdentities,
				action.SellerID,
				action.VendorCodes(),
				action.TransferID,
				action.ActionID,
			); err != nil {
				return fmt.Errorf("block uncertain publication identities: %w", err)
			}
		}
		return nil
	}

	memberResults := make(map[int64]cardpublication_execution.MemberSubmissionResult)
	for _, memberResult := range result.MemberResults {
		memberResults[memberResult.ActionMemberID] = memberResult
	}
	if len(memberResults) != len(action.Members) {
		return cardpublication_execution.ErrProductActionConflict
	}
	actionClass := transfer_model.ResultRejected
	for _, member := range action.Members {
		memberResult, exists := memberResults[member.ID]
		if !exists {
			return cardpublication_execution.ErrProductActionConflict
		}
		if memberResult.OutcomeClass == transfer_model.ResultInternalError {
			actionClass = transfer_model.ResultInternalError
		}
		const finishMember = `
			UPDATE wb.publication_action_members
			SET outcome_class = $4,
			    outcome_code = $5,
			    finished_at = CURRENT_TIMESTAMP,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND action_id = $2
				AND id = $3
				AND outcome_class IS NULL;
		`
		update, err := tx.Exec(
			ctx,
			finishMember,
			action.TransferID,
			action.ActionID,
			member.ID,
			memberResult.OutcomeClass,
			memberResult.OutcomeCode,
		)
		if err != nil {
			return fmt.Errorf("finish publication product member: %w", err)
		}
		if update.RowsAffected() != 1 {
			return cardpublication_execution.ErrProductActionConflict
		}
		identityState := "rejected"
		clearActive := true
		if memberResult.OutcomeClass == transfer_model.ResultInternalError {
			identityState = "blocked_uncertain"
			clearActive = false
		}
		const finishIdentity = `
			UPDATE wb.product_identities
			SET state = $5,
			    active_transfer_id = CASE WHEN $6 THEN NULL ELSE active_transfer_id END,
			    active_action_id = CASE WHEN $6 THEN NULL ELSE active_action_id END,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE seller_id = $1
				AND vendor_code_key = $2
				AND active_transfer_id = $3
				AND active_action_id = $4;
		`
		update, err = tx.Exec(
			ctx,
			finishIdentity,
			action.SellerID,
			member.VendorCode,
			action.TransferID,
			action.ActionID,
			identityState,
			clearActive,
		)
		if err != nil {
			return fmt.Errorf("finish publication product identity: %w", err)
		}
		if update.RowsAffected() != 1 {
			return cardpublication_execution.ErrProductActionConflict
		}
	}
	if err := finishPotentialMediaActions(
		ctx,
		tx,
		action,
		"VENDOR_CODE_ATTRIBUTION_UNAVAILABLE",
	); err != nil {
		return err
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
			AND state = 'dispatching';
	`
	update, err = tx.Exec(
		ctx,
		finishAction,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		actionClass,
		result.SafeCode,
	)
	if err != nil {
		return fmt.Errorf("finish publication product action: %w", err)
	}
	if update.RowsAffected() != 1 {
		return cardpublication_execution.ErrProductActionConflict
	}
	return reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID)
}
