package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
)

// BeginMediaAttempt начинает попытку исполнения медиа-действия, связывая идентичность товара и создавая запись попытки.
func (repository *Repository) BeginMediaAttempt(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_service.BeginMediaAttemptCommand,
) (cardpublication_service.MediaAttempt, error) {
	action := command.Action
	if tx == nil || action.State != "planned" || action.AttemptID != 0 ||
		command.Authorization.AuthorizationID != action.AuthorizationID ||
		command.Authorization.Revision != action.AuthorizationRevision ||
		command.Baseline.Validate() != nil ||
		command.Baseline.TransferID != action.TransferID ||
		command.Baseline.ActionID != action.ActionID ||
		command.Baseline.CabinetID != action.CabinetID ||
		command.RecheckObservationID <= 0 || command.RequestDigest == (cardpublication_service.Digest{}) ||
		len(command.RequestPayload) == 0 {
		return cardpublication_service.MediaAttempt{},
			errors.New("begin publication media attempt command is invalid")
	}
	request, payload, digest, err := action.FinalRequest()
	if err != nil || request.NMID != action.NMID || digest != command.RequestDigest ||
		!equalBytes(payload, command.RequestPayload) {
		return cardpublication_service.MediaAttempt{},
			cardpublication_service.ErrMediaActionConflict
	}
	const bindIdentity = `
		UPDATE wb.product_identities
		SET active_transfer_id = $3,
		    active_action_id = $4,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE cabinet_id = $1
			AND vendor_code_key = $2
			AND state = 'remote_present'
			AND nm_id = $5
			AND revision = $6
			AND active_action_id IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		bindIdentity,
		action.CabinetID,
		action.Member.VendorCode,
		action.TransferID,
		action.ActionID,
		action.NMID,
		action.IdentityRevision,
	)
	if err != nil {
		return cardpublication_service.MediaAttempt{},
			fmt.Errorf("bind publication media identity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaAttempt{},
			cardpublication_service.ErrMediaActionConflict
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
			AND action.kind = 'upload_media'
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
		return cardpublication_service.MediaAttempt{},
			fmt.Errorf("begin publication media action: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaAttempt{},
			cardpublication_service.ErrMediaActionConflict
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
		return cardpublication_service.MediaAttempt{},
			fmt.Errorf("touch publication plan for media: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaAttempt{},
			cardpublication_service.ErrMediaActionConflict
	}
	const insertAttempt = `
		INSERT INTO wb.publication_attempts (
			transfer_id, action_id, authorization_id, recheck_observation_id,
			baseline_cabinet_id, baseline_cursor_revision,
			baseline_cursor_updated_at, baseline_cursor_batch_uuid,
			baseline_captured_at, request_digest, request_payload,
			attribution_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, started_at;
	`
	attempt := cardpublication_service.MediaAttempt{
		TransferID:           action.TransferID,
		ActionID:             action.ActionID,
		AuthorizationID:      action.AuthorizationID,
		RecheckObservationID: command.RecheckObservationID,
		AttributionID:        action.AttributionID,
		RequestDigest:        command.RequestDigest,
		RequestPayload:       append([]byte(nil), command.RequestPayload...),
	}
	if err := tx.QueryRow(
		ctx,
		insertAttempt,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		command.RecheckObservationID,
		command.Baseline.CabinetID,
		command.Baseline.CursorRevision,
		nullableTime(command.Baseline.CursorUpdatedAt),
		command.Baseline.CursorBatchUUID,
		command.Baseline.CapturedAt,
		command.RequestDigest[:],
		command.RequestPayload,
		nullablePositive(action.AttributionID),
	).Scan(&attempt.ID, &attempt.StartedAt); err != nil {
		return cardpublication_service.MediaAttempt{},
			fmt.Errorf("insert publication media attempt: %w", err)
	}
	return attempt, nil
}

// FinishMediaWithoutAttempt завершает медиа-действие без попытки (например, пропуск).
func (repository *Repository) FinishMediaWithoutAttempt(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.MediaAction,
	safeCode string,
) (cardpublication_service.MediaGroupResult, error) {
	if tx == nil || action.State != "planned" || action.AttemptID != 0 ||
		safeCode == "" || len(safeCode) > 128 {
		return cardpublication_service.MediaGroupResult{},
			errors.New("finish publication media without attempt command is invalid")
	}
	const finishMember = `
		UPDATE wb.publication_action_members
		SET outcome_class = 'skipped',
		    outcome_code = $4,
		    nm_id = $5,
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
		safeCode,
		action.NMID,
	)
	if err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("skip publication media member: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
	}
	const finishAction = `
		UPDATE wb.publication_actions
		SET state = 'terminal',
		    outcome_class = 'skipped',
		    outcome_code = $3,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND kind = 'upload_media'
			AND state = 'planned'
			AND authorization_id IS NULL;
	`
	result, err = tx.Exec(ctx, finishAction, action.TransferID, action.ActionID, safeCode)
	if err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("skip publication media action: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
	}
	if err := reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID); err != nil {
		return cardpublication_service.MediaGroupResult{}, err
	}
	return loadMediaGroupResult(ctx, tx, action)
}
