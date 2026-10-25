package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

// ListReconcilingProductActions возвращает действия в сверке с учётом интервалов опроса WB.
func (repository *Repository) ListReconcilingProductActions(
	ctx context.Context,
	now time.Time,
	baseDelay time.Duration,
	limit int,
) ([]cardpublication_execution.ProductActionCandidate, error) {
	if now.IsZero() || baseDelay <= 0 || limit <= 0 || limit > 100 {
		return nil, errors.New("reconciling product action query is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		WITH reconciling AS (
		SELECT
			action.transfer_id AS transfer_id,
			action.id AS action_id,
			action.target_id AS target_id,
			target.seller_id AS seller_id,
			live_auth.id AS authorization_id,
			live_auth.revision AS authorization_revision,
			plan.id AS plan_id,
			ROW_NUMBER() OVER (
				PARTITION BY target.seller_id
				ORDER BY action.updated_at, action.id
			) AS cabinet_rank,
			action.updated_at,
			ARRAY(
				SELECT member.vendor_code
				FROM wb.publication_action_members AS member
				WHERE member.transfer_id = action.transfer_id
					AND member.action_id = action.id
					AND member.outcome_class IS NULL
				ORDER BY member.request_member_index
			) AS catalog_vendor_codes
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.id = action.authorization_id
		JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		LEFT JOIN wb.publication_product_retries AS retry
		  ON retry.transfer_id = action.transfer_id
		 AND retry.action_id = action.id
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'reconciling'
			AND attempt.finished_at IS NOT NULL
			AND EXISTS (
				SELECT 1
				FROM wb.publication_action_members AS pending_member
				WHERE pending_member.transfer_id = action.transfer_id
					AND pending_member.action_id = action.id
					AND pending_member.outcome_class IS NULL
			)
				AND action.updated_at <= $1::timestamptz - (
					make_interval(secs => $2::double precision) *
					CASE
						WHEN $1::timestamptz - COALESCE(retry.started_at, attempt.started_at) >= INTERVAL '5 minutes' THEN 6
						WHEN $1::timestamptz - COALESCE(retry.started_at, attempt.started_at) >= INTERVAL '1 minute' THEN 3
					ELSE 1
				END
			)
		)
		SELECT
			transfer_id, action_id, target_id, seller_id,
			authorization_id, authorization_revision,
			plan_id, catalog_vendor_codes
		FROM reconciling
		ORDER BY cabinet_rank, updated_at, seller_id, action_id
		LIMIT $3;
	`
	rows, err := repository.pool.Query(ctx, query, now, baseDelay.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("list reconciling product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_execution.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanCatalogProductActionCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reconciling product actions: %w", err)
	}
	return result, nil
}

// LoadErrorBatchMatches находит совпадения по отклонённым вендор-кодам в буфере ошибок WB (Error List).
func (repository *Repository) LoadErrorBatchMatches(
	ctx context.Context,
	action cardpublication_execution.ProductAction,
) ([]cardpublication_execution.ErrorBatchMatch, error) {
	if action.AttemptID <= 0 {
		return nil, cardpublication_execution.ErrProductActionConflict
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT
			batch.id,
			batch.batch_uuid,
			batch.batch_updated_at,
			batch.rejected_vendor_codes,
			batch.error_codes
		FROM wb.publication_attempts AS attempt
		JOIN wb.publication_error_batches AS batch
		  ON batch.seller_id = attempt.baseline_seller_id
		WHERE attempt.transfer_id = $1
			AND attempt.id = $2
			AND attempt.action_id = $3
			AND batch.rejected_vendor_codes && $4::text[]
			AND (
				attempt.baseline_cursor_updated_at IS NULL
				OR batch.batch_updated_at > attempt.baseline_cursor_updated_at
				OR (
					batch.batch_updated_at = attempt.baseline_cursor_updated_at
					AND batch.batch_uuid > attempt.baseline_cursor_batch_uuid
				)
			)
		ORDER BY batch.batch_updated_at, batch.batch_uuid, batch.id;
	`
	rows, err := repository.pool.Query(
		ctx,
		query,
		action.TransferID,
		action.AttemptID,
		action.ActionID,
		action.PendingVendorCodes(),
	)
	if err != nil {
		return nil, fmt.Errorf("load publication Error List matches: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_execution.ErrorBatchMatch, 0)
	for rows.Next() {
		var match cardpublication_execution.ErrorBatchMatch
		if err := rows.Scan(
			&match.ID,
			&match.BatchUUID,
			&match.UpdatedAt,
			&match.RejectedVendorCodes,
			&match.ErrorCodes,
		); err != nil {
			return nil, fmt.Errorf("scan publication Error List match: %w", err)
		}
		match.UpdatedAt = match.UpdatedAt.UTC()
		result = append(result, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// RecordProductReconciliationPoll фиксирует промежуточные результаты опроса сверки карточек без завершения действия.
func (repository *Repository) RecordProductReconciliationPoll(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
	postObservationID int64,
	memberResults []cardpublication_execution.MemberReconciliationResult,
) error {
	if tx == nil || action.State != "reconciling" || action.AttemptID <= 0 ||
		postObservationID <= 0 {
		return errors.New("record product reconciliation poll command is invalid")
	}
	if err := cardpublication_execution.ValidateMemberReconciliationResults(
		action,
		memberResults,
	); err != nil {
		return err
	}
	if err := recordProductReconciliationMembers(
		ctx,
		tx,
		action,
		postObservationID,
		memberResults,
	); err != nil {
		return err
	}
	const update = `
		UPDATE wb.publication_actions AS action
		SET updated_at = CURRENT_TIMESTAMP
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.authorization_id = $3
			AND action.state = 'reconciling'
			AND EXISTS (
				SELECT 1
				FROM wb.publication_attempts AS attempt
				WHERE attempt.transfer_id = action.transfer_id
					AND attempt.action_id = action.id
					AND attempt.id = $4
					AND attempt.finished_at IS NOT NULL
			)
			AND EXISTS (
				SELECT 1
				FROM wb.publication_observations AS observation
				WHERE observation.transfer_id = action.transfer_id
					AND observation.id = $5
					AND observation.target_id = action.target_id
					AND observation.kind = 'post_submission'
			);
	`
	result, err := tx.Exec(
		ctx,
		update,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		action.AttemptID,
		postObservationID,
	)
	if err != nil {
		return fmt.Errorf("record product reconciliation poll: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_execution.ErrProductActionConflict
	}
	return nil
}

// FinishProductReconciliation завершает сверку действия с товарами, фиксируя финальные исходы элементов и действия.
func (repository *Repository) FinishProductReconciliation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
	postObservationID int64,
	memberResults []cardpublication_execution.MemberReconciliationResult,
	actionClass transfer_model.ResultClass,
	actionCode string,
) error {
	if tx == nil || action.State != "reconciling" || action.AttemptID <= 0 ||
		postObservationID <= 0 || !actionClass.IsValid() || actionCode == "" ||
		len(actionCode) > 128 {
		return errors.New("finish product reconciliation command is invalid")
	}
	if err := cardpublication_execution.ValidateMemberReconciliationResults(
		action,
		memberResults,
	); err != nil {
		return err
	}
	if err := recordProductReconciliationMembers(
		ctx,
		tx,
		action,
		postObservationID,
		memberResults,
	); err != nil {
		return err
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
		UPDATE wb.publication_actions AS action
		SET state = 'terminal',
		    outcome_class = $4,
		    outcome_code = $5,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.authorization_id = $3
			AND action.state = 'reconciling'
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_action_members AS member
				WHERE member.transfer_id = action.transfer_id
					AND member.action_id = action.id
					AND member.outcome_class IS NULL
			);
	`
	updated, err := tx.Exec(
		ctx,
		finishAction,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		actionClass,
		actionCode,
	)
	if err != nil {
		return fmt.Errorf("finish reconciled publication action: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return cardpublication_execution.ErrProductActionConflict
	}
	return reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID)
}

// recordProductReconciliationMembers обновляет статусы элементов действия, идентичностей товаров и привязки attributions.
func recordProductReconciliationMembers(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
	postObservationID int64,
	memberResults []cardpublication_execution.MemberReconciliationResult,
) error {
	members := make(map[int64]cardpublication_execution.ProductActionMember, len(action.Members))
	for _, member := range action.Members {
		members[member.ID] = member
	}
	for _, result := range memberResults {
		member, exists := members[result.ActionMemberID]
		if !exists || member.OutcomeClass != "" {
			return cardpublication_execution.ErrProductActionConflict
		}
		var nmID any
		if result.NMID > 0 {
			nmID = result.NMID
		}
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
		updated, err := tx.Exec(
			ctx,
			finishMember,
			action.TransferID,
			action.ActionID,
			member.ID,
			result.OutcomeClass,
			result.OutcomeCode,
			nmID,
		)
		if err != nil {
			return fmt.Errorf("finish reconciled publication member: %w", err)
		}
		if updated.RowsAffected() != 1 {
			return cardpublication_execution.ErrProductActionConflict
		}

		identityState := "blocked_uncertain"
		clearActive := false
		if result.OutcomeClass == transfer_service.ResultRejected {
			identityState = "rejected"
			clearActive = true
		} else if result.NMID > 0 && result.IMTID > 0 && result.SubjectID > 0 {
			identityState = "remote_present"
			clearActive = true
		}
		const finishIdentity = `
			UPDATE wb.product_identities
			SET state = $5,
			    nm_id = $6,
			    imt_id = $7,
			    subject_id = $8,
			    active_transfer_id = CASE WHEN $9 THEN NULL ELSE active_transfer_id END,
			    active_action_id = CASE WHEN $9 THEN NULL ELSE active_action_id END,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE seller_id = $1
				AND vendor_code_key = $2
				AND active_transfer_id = $3
				AND active_action_id = $4;
		`
		updated, err = tx.Exec(
			ctx,
			finishIdentity,
			action.SellerID,
			member.VendorCode,
			action.TransferID,
			action.ActionID,
			identityState,
			nullablePositive(result.NMID),
			nullablePositive(result.IMTID),
			nullablePositive(result.SubjectID),
			clearActive,
		)
		if err != nil {
			return fmt.Errorf("finish reconciled publication identity: %w", err)
		}
		if updated.RowsAffected() != 1 {
			return cardpublication_execution.ErrProductActionConflict
		}

		if result.AttributionLevel == "observed_after_attempt" {
			const insertAttribution = `
				INSERT INTO wb.publication_attributions (
					transfer_id, group_target_id, action_id, action_member_id,
					seller_id, nm_id, attempt_id,
					preflight_observation_id, post_observation_id, level,
					safe_reason_code
				)
				VALUES (
					$1, $2, $3, $4, $5, $6, $7, $8, $9,
					'observed_after_attempt', $10
				);
			`
			if _, err := tx.Exec(
				ctx,
				insertAttribution,
				action.TransferID,
				member.GroupTargetID,
				action.ActionID,
				member.ID,
				action.SellerID,
				result.NMID,
				action.AttemptID,
				action.PreflightObservationID,
				postObservationID,
				result.OutcomeCode,
			); err != nil {
				return fmt.Errorf("insert publication attribution evidence: %w", err)
			}
		}

		if result.ErrorBatchID > 0 {
			const insertCorrelation = `
				INSERT INTO wb.publication_error_correlations (
					transfer_id, action_id, action_member_id, attempt_id,
					error_batch_id, safe_result_code
				)
				SELECT $1, $2, $3, $4, batch.id, $6
				FROM wb.publication_error_batches AS batch
				JOIN wb.publication_attempts AS attempt
				  ON attempt.transfer_id = $1
				 AND attempt.id = $4
				 AND attempt.action_id = $2
				WHERE batch.id = $5
					AND batch.seller_id = attempt.baseline_seller_id
					AND batch.rejected_vendor_codes @> ARRAY[$7]::text[]
					AND (
						attempt.baseline_cursor_updated_at IS NULL
						OR batch.batch_updated_at > attempt.baseline_cursor_updated_at
						OR (
							batch.batch_updated_at = attempt.baseline_cursor_updated_at
							AND batch.batch_uuid > attempt.baseline_cursor_batch_uuid
						)
					)
				RETURNING id;
			`
			var correlationID int64
			if err := tx.QueryRow(
				ctx,
				insertCorrelation,
				action.TransferID,
				action.ActionID,
				member.ID,
				action.AttemptID,
				result.ErrorBatchID,
				result.OutcomeCode,
				member.VendorCode,
			).Scan(&correlationID); err != nil {
				return fmt.Errorf("insert publication Error List correlation: %w", err)
			}
		}
	}
	return nil
}

// finishPotentialMediaActions пропускает медиа-действия, если товарное действие завершилось ошибкой или без атрибуции nmID.
func finishPotentialMediaActions(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
	safeCode string,
) error {
	itemTargetIDs := make([]int64, len(action.Members))
	for index, member := range action.Members {
		itemTargetIDs[index] = member.TransferItemTargetID
	}
	const finishMembers = `
		UPDATE wb.publication_action_members AS member
		SET outcome_class = 'skipped',
		    outcome_code = $4,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.publication_actions AS media_action
		WHERE member.transfer_id = $1
			AND media_action.transfer_id = member.transfer_id
			AND media_action.id = member.action_id
			AND media_action.plan_id = $2
			AND media_action.kind = 'upload_media'
			AND media_action.state = 'planned'
			AND member.transfer_item_target_id = ANY($3::bigint[])
			AND member.outcome_class IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_action_members AS product_member
				JOIN wb.publication_actions AS product_action
				  ON product_action.transfer_id = product_member.transfer_id
				 AND product_action.id = product_member.action_id
				JOIN wb.publication_attributions AS attribution
				  ON attribution.transfer_id = product_member.transfer_id
				 AND attribution.action_member_id = product_member.id
					WHERE product_member.transfer_id = member.transfer_id
					  AND product_member.transfer_item_target_id
					      = member.transfer_item_target_id
					  AND product_member.vendor_code = member.vendor_code
					  AND product_action.plan_id = media_action.plan_id
					  AND product_action.kind IN ('create_group', 'add_to_group')
					  AND attribution.level IN ('direct', 'observed_after_attempt')
					  AND attribution.seller_id = $5
					  AND attribution.group_target_id = member.group_target_id
				);
	`
	if _, err := tx.Exec(
		ctx,
		finishMembers,
		action.TransferID,
		action.PlanID,
		itemTargetIDs,
		safeCode,
		action.SellerID,
	); err != nil {
		return fmt.Errorf("skip publication media members: %w", err)
	}
	const finishActions = `
		UPDATE wb.publication_actions AS media_action
		SET state = 'terminal',
		    outcome_class = 'skipped',
		    outcome_code = $4,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = media_action.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE media_action.transfer_id = $1
			AND media_action.plan_id = $2
			AND media_action.kind = 'upload_media'
			AND media_action.state = 'planned'
			AND EXISTS (
				SELECT 1
				FROM wb.publication_action_members AS member
				WHERE member.transfer_id = media_action.transfer_id
				  AND member.action_id = media_action.id
				  AND member.transfer_item_target_id = ANY($3::bigint[])
			)
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_action_members AS member
				WHERE member.transfer_id = media_action.transfer_id
				  AND member.action_id = media_action.id
				  AND member.outcome_class IS NULL
			);
	`
	if _, err := tx.Exec(
		ctx,
		finishActions,
		action.TransferID,
		action.PlanID,
		itemTargetIDs,
		safeCode,
	); err != nil {
		return fmt.Errorf("skip publication media actions: %w", err)
	}
	return nil
}

// PendingMediaGroupTargetIDs возвращает список идентификаторов целевых групп, ожидающих загрузки медиа.
func (repository *Repository) PendingMediaGroupTargetIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
) ([]int64, error) {
	if tx == nil || action.TransferID <= 0 || action.PlanID <= 0 || len(action.Members) == 0 {
		return nil, cardpublication_execution.ErrProductActionConflict
	}
	itemTargetIDs := make([]int64, len(action.Members))
	for index, member := range action.Members {
		itemTargetIDs[index] = member.TransferItemTargetID
	}
	const query = `
		SELECT DISTINCT media_member.group_target_id
		FROM wb.publication_actions AS media_action
		JOIN wb.publication_action_members AS media_member
		  ON media_member.transfer_id = media_action.transfer_id
		 AND media_member.action_id = media_action.id
		WHERE media_action.transfer_id = $1
			AND media_action.plan_id = $2
			AND media_action.kind = 'upload_media'
			AND media_action.state = 'planned'
			AND media_member.outcome_class IS NULL
			AND media_member.transfer_item_target_id = ANY($3::bigint[])
		ORDER BY media_member.group_target_id;
	`
	rows, err := tx.Query(ctx, query, action.TransferID, action.PlanID, itemTargetIDs)
	if err != nil {
		return nil, fmt.Errorf("query pending publication media groups: %w", err)
	}
	defer rows.Close()
	result := make([]int64, 0)
	for rows.Next() {
		var groupTargetID int64
		if err := rows.Scan(&groupTargetID); err != nil {
			return nil, fmt.Errorf("scan pending publication media group: %w", err)
		}
		result = append(result, groupTargetID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending publication media groups: %w", err)
	}
	return result, nil
}
