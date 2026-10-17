package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

// PlanAttemptCount подсчитывает количество попыток публикации для плана.
func (repository *Repository) PlanAttemptCount(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	planID int64,
) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wb.publication_attempts AS attempt
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = attempt.transfer_id
		 AND action.id = attempt.action_id
		WHERE action.transfer_id = $1 AND action.plan_id = $2;
	`
	var count int
	if err := tx.QueryRow(ctx, query, transferID, planID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count publication plan attempts: %w", err)
	}
	return count, nil
}

// SupersedePlanBeforeDispatch отменяет план и его действия до диспетчеризации (например, при появлении новой авторизации).
func (repository *Repository) SupersedePlanBeforeDispatch(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
	safeCode string,
) error {
	if tx == nil || safeCode == "" || len(safeCode) > 128 {
		return errors.New("supersede publication plan command is invalid")
	}
	const countQuery = `
		SELECT COUNT(*)
		FROM wb.publication_actions
		WHERE transfer_id = $1 AND plan_id = $2;
	`
	var expectedActions int64
	if err := tx.QueryRow(ctx, countQuery, action.TransferID, action.PlanID).Scan(&expectedActions); err != nil {
		return fmt.Errorf("count superseded publication actions: %w", err)
	}
	const finishMembers = `
		UPDATE wb.publication_action_members AS member
		SET outcome_class = 'skipped',
		    outcome_code = $3,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.publication_actions AS action
		WHERE member.transfer_id = $1
			AND action.transfer_id = member.transfer_id
			AND action.id = member.action_id
			AND action.plan_id = $2
			AND member.outcome_class IS NULL;
	`
	if _, err := tx.Exec(ctx, finishMembers, action.TransferID, action.PlanID, safeCode); err != nil {
		return fmt.Errorf("supersede publication action members: %w", err)
	}
	const finishActions = `
		UPDATE wb.publication_actions
		SET state = 'superseded',
		    outcome_class = 'skipped',
		    outcome_code = $3,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND plan_id = $2
			AND state = 'planned';
	`
	result, err := tx.Exec(ctx, finishActions, action.TransferID, action.PlanID, safeCode)
	if err != nil {
		return fmt.Errorf("supersede publication actions: %w", err)
	}
	if result.RowsAffected() != expectedActions {
		return cardpublication_service.ErrProductActionConflict
	}
	const finishPlan = `
		UPDATE wb.publication_plans
		SET state = 'superseded',
		    outcome_class = 'skipped',
		    outcome_code = $3,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND state = 'awaiting_authorization';
	`
	result, err = tx.Exec(ctx, finishPlan, action.TransferID, action.PlanID, safeCode)
	if err != nil {
		return fmt.Errorf("supersede publication plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ErrProductActionConflict
	}
	return nil
}

// AbortPlanAfterDispatch аварийно отменяет запланированные действия плана после частичной диспетчеризации.
func (repository *Repository) AbortPlanAfterDispatch(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
	safeCode string,
) ([]cardpublication_service.AbortedProductAction, error) {
	if tx == nil || action.State != "planned" || safeCode == "" || len(safeCode) > 128 {
		return nil, errors.New("abort publication plan command is invalid")
	}
	const load = `
		SELECT
			product_action.id,
			member.id,
			member.group_target_id,
			member.transfer_item_target_id,
			member.request_member_index,
			member.vendor_code
		FROM wb.publication_actions AS product_action
		JOIN wb.publication_action_members AS member
		  ON member.transfer_id = product_action.transfer_id
		 AND member.action_id = product_action.id
		WHERE product_action.transfer_id = $1
			AND product_action.plan_id = $2
			AND product_action.kind IN ('create_group', 'add_to_group')
			AND product_action.state = 'planned'
		ORDER BY product_action.id, member.request_member_index
		FOR UPDATE OF product_action, member;
	`
	rows, err := tx.Query(ctx, load, action.TransferID, action.PlanID)
	if err != nil {
		return nil, fmt.Errorf("lock aborted publication plan actions: %w", err)
	}
	byAction := make(map[int64]*cardpublication_service.AbortedProductAction)
	order := make([]int64, 0)
	for rows.Next() {
		var actionID int64
		var member cardpublication_service.ProductActionMember
		if err := rows.Scan(
			&actionID,
			&member.ID,
			&member.GroupTargetID,
			&member.TransferItemTargetID,
			&member.RequestMemberIndex,
			&member.VendorCode,
		); err != nil {
			rows.Close()
			return nil, err
		}
		aborted := byAction[actionID]
		if aborted == nil {
			aborted = &cardpublication_service.AbortedProductAction{ActionID: actionID}
			byAction[actionID] = aborted
			order = append(order, actionID)
		}
		aborted.Members = append(aborted.Members, member)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(order) == 0 {
		return nil, cardpublication_service.ErrProductActionConflict
	}

	const blockIdentities = `
		UPDATE wb.product_identities AS identity
		SET state = 'blocked_uncertain',
		    revision = identity.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE identity.active_action_id IS NULL
			AND identity.state = 'remote_missing'
			AND EXISTS (
				SELECT 1
				FROM wb.publication_actions AS product_action
				JOIN wb.publication_action_members AS member
				  ON member.transfer_id = product_action.transfer_id
				 AND member.action_id = product_action.id
				JOIN wb.transfer_targets AS target
				  ON target.transfer_id = product_action.transfer_id
				 AND target.id = product_action.target_id
				WHERE product_action.transfer_id = $1
				  AND product_action.plan_id = $2
				  AND product_action.kind IN ('create_group', 'add_to_group')
				  AND product_action.state = 'planned'
				  AND identity.cabinet_id = target.cabinet_id
				  AND identity.vendor_code_key = member.vendor_code
			);
	`
	if _, err := tx.Exec(ctx, blockIdentities, action.TransferID, action.PlanID); err != nil {
		return nil, fmt.Errorf("block aborted publication identities: %w", err)
	}
	const finishMembers = `
		UPDATE wb.publication_action_members AS member
		SET outcome_class = CASE
				WHEN publication_action.kind = 'upload_media' THEN 'skipped'
				ELSE 'unresolved'
			END,
		    outcome_code = CASE
					WHEN publication_action.kind = 'upload_media'
						THEN 'VENDOR_CODE_ATTRIBUTION_UNAVAILABLE'
				ELSE $3
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.publication_actions AS publication_action
		WHERE member.transfer_id = $1
			AND publication_action.transfer_id = member.transfer_id
			AND publication_action.id = member.action_id
			AND publication_action.plan_id = $2
			AND publication_action.state = 'planned'
			AND member.outcome_class IS NULL;
	`
	if _, err := tx.Exec(ctx, finishMembers, action.TransferID, action.PlanID, safeCode); err != nil {
		return nil, fmt.Errorf("finish aborted publication members: %w", err)
	}
	const finishActions = `
		UPDATE wb.publication_actions
		SET state = 'terminal',
		    outcome_class = CASE
				WHEN kind = 'upload_media' THEN 'skipped'
				ELSE 'unresolved'
			END,
		    outcome_code = CASE
				WHEN kind = 'upload_media' THEN 'VENDOR_CODE_ATTRIBUTION_UNAVAILABLE'
				ELSE $3
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND plan_id = $2
			AND state = 'planned';
	`
	if _, err := tx.Exec(ctx, finishActions, action.TransferID, action.PlanID, safeCode); err != nil {
		return nil, fmt.Errorf("finish aborted publication actions: %w", err)
	}
	if err := reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID); err != nil {
		return nil, err
	}
	result := make([]cardpublication_service.AbortedProductAction, 0, len(order))
	for _, actionID := range order {
		result = append(result, *byAction[actionID])
	}
	return result, nil
}

// PublicationPlanTerminal проверяет, перешёл ли план публикации в терминальное состояние со всеми действиями.
func (repository *Repository) PublicationPlanTerminal(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	planID int64,
) (bool, error) {
	if tx == nil || transferID <= 0 || planID <= 0 {
		return false, errors.New("publication terminal plan query is invalid")
	}
	const query = `
		SELECT
			plan.state = 'terminal'
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_actions AS action
				WHERE action.transfer_id = plan.transfer_id
					AND action.plan_id = plan.id
					AND action.state NOT IN ('terminal', 'superseded')
			)
		FROM wb.publication_plans AS plan
		WHERE plan.transfer_id = $1 AND plan.id = $2;
	`
	var terminal bool
	if err := tx.QueryRow(ctx, query, transferID, planID).Scan(&terminal); err != nil {
		return false, fmt.Errorf("read publication terminal plan: %w", err)
	}
	return terminal, nil
}

// reducePublicationPlan вычисляет и фиксирует терминальное состояние плана публикации, когда все действия завершены.
func reducePublicationPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	planID int64,
) error {
	const lockPlan = `
		SELECT state
		FROM wb.publication_plans
		WHERE transfer_id = $1 AND id = $2
		FOR UPDATE;
	`
	var state string
	if err := tx.QueryRow(ctx, lockPlan, transferID, planID).Scan(&state); err != nil {
		return fmt.Errorf("lock publication plan reduction: %w", err)
	}
	if state != "executing" {
		return nil
	}
	const hasUnfinished = `
		SELECT EXISTS (
			SELECT 1
			FROM wb.publication_actions
			WHERE transfer_id = $1
				AND plan_id = $2
				AND state NOT IN ('terminal', 'superseded')
		);
	`
	var unfinished bool
	if err := tx.QueryRow(ctx, hasUnfinished, transferID, planID).Scan(&unfinished); err != nil {
		return fmt.Errorf("check unfinished publication actions: %w", err)
	}
	if unfinished {
		return nil
	}
	const reduce = `
		WITH action_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state IN ('terminal', 'superseded')) AS terminal,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
			FROM wb.publication_actions
			WHERE transfer_id = $1 AND plan_id = $2
		)
		UPDATE wb.publication_plans AS plan
		SET state = 'terminal',
		    outcome_class = CASE
				WHEN action_counts.unresolved > 0 OR action_counts.internal_error > 0
					THEN 'unresolved'
				WHEN action_counts.rejected = action_counts.total THEN 'rejected'
				WHEN action_counts.rejected > 0 AND action_counts.accepted > 0 THEN 'partial'
				ELSE 'success'
			END,
		    outcome_code = CASE
				WHEN action_counts.unresolved > 0 OR action_counts.internal_error > 0
					THEN 'PUBLICATION_REQUIRES_ATTENTION'
				WHEN action_counts.rejected = action_counts.total THEN 'PUBLICATION_REJECTED'
				WHEN action_counts.rejected > 0 AND action_counts.accepted > 0
					THEN 'PUBLICATION_PARTIAL'
				ELSE 'PUBLICATION_TERMINAL'
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = plan.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM action_counts
		WHERE plan.transfer_id = $1
			AND plan.id = $2
			AND plan.state IN ('awaiting_authorization', 'executing')
			AND action_counts.total > 0
			AND action_counts.terminal = action_counts.total;
	`
	if _, err := tx.Exec(ctx, reduce, transferID, planID); err != nil {
		return fmt.Errorf("reduce publication plan: %w", err)
	}
	return nil
}
