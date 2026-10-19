package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
)

// ListDispatchableMediaActions возвращает список медиа-действий, готовых к диспетчеризации.
func (repository *Repository) ListDispatchableMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("dispatchable media action limit is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		WITH dispatchable AS (
			SELECT
				action.transfer_id,
				action.id AS action_id,
				action.target_id,
				target.seller_id,
				live_auth.id AS authorization_id,
				live_auth.revision AS authorization_revision,
				plan.plan_digest,
				plan.target_set_root,
				target.position AS target_position,
				ROW_NUMBER() OVER (
					PARTITION BY target.seller_id
					ORDER BY transfer.id, action.id
				) AS cabinet_rank
			FROM wb.publication_actions AS action
			JOIN wb.publication_plans AS plan
			  ON plan.transfer_id = action.transfer_id
			 AND plan.id = action.plan_id
			JOIN wb.transfers AS transfer
			  ON transfer.id = action.transfer_id
			JOIN wb.transfer_targets AS target
			  ON target.transfer_id = action.transfer_id
			 AND target.id = action.target_id
			JOIN wb.transfer_live_authorizations AS live_auth
			  ON live_auth.transfer_id = action.transfer_id
			 AND live_auth.plan_id = action.plan_id
			 AND live_auth.plan_digest = plan.plan_digest
			JOIN wb.publication_action_members AS media_member
			  ON media_member.transfer_id = action.transfer_id
			 AND media_member.action_id = action.id
			LEFT JOIN wb.publication_action_members AS product_member
			  ON product_member.transfer_id = media_member.transfer_id
			 AND product_member.transfer_item_target_id
			     = media_member.transfer_item_target_id
			LEFT JOIN wb.publication_actions AS product_action
			  ON product_action.transfer_id = product_member.transfer_id
			 AND product_action.id = product_member.action_id
			 AND product_action.plan_id = action.plan_id
			LEFT JOIN wb.publication_attributions AS attribution
			  ON attribution.transfer_id = product_member.transfer_id
			 AND attribution.action_member_id = product_member.id
			 AND attribution.level IN ('direct', 'observed_after_attempt')
			 AND attribution.plan_digest = plan.plan_digest
			 AND attribution.seller_id = target.seller_id
			 AND attribution.group_target_id = media_member.group_target_id
			JOIN wb.product_identities AS identity
			  ON identity.seller_id = target.seller_id
			 AND identity.vendor_code_key = media_member.vendor_code
			 AND identity.state = 'remote_present'
			 AND identity.nm_id IS NOT NULL
			 AND (attribution.nm_id IS NULL OR identity.nm_id = attribution.nm_id)
			 AND identity.active_action_id IS NULL
			WHERE action.kind = 'upload_media'
				AND action.state = 'planned'
				AND action.authorization_id IS NULL
				AND media_member.outcome_class IS NULL
				AND (
					(product_action.id IS NOT NULL
					 AND product_action.kind IN ('create_group', 'add_to_group')
					 AND product_action.state = 'terminal'
					 AND plan.state = 'executing'
					 AND transfer.phase IN ('reconciling', 'media'))
					OR
					(product_action.id IS NULL
					 AND plan.state IN ('awaiting_authorization', 'executing')
					 AND transfer.phase IN ('awaiting_authorization', 'publishing', 'reconciling', 'media'))
				)
				AND transfer.outcome = 'running'
				AND live_auth.state = 'authorized'
				AND live_auth.expires_at > CURRENT_TIMESTAMP
				AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_attempts AS attempt
					WHERE attempt.action_id = action.id
				)
		)
		SELECT
			transfer_id, action_id, target_id, seller_id,
			authorization_id, authorization_revision, plan_digest, target_set_root
		FROM dispatchable
		ORDER BY cabinet_rank, target_position, seller_id, action_id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list dispatchable publication media actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.MediaActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dispatchable publication media action: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dispatchable publication media actions: %w", err)
	}
	return result, nil
}

// ListInterruptedMediaActions возвращает прерванные медиа-действия, находящиеся в состоянии dispatching без завершения.
func (repository *Repository) ListInterruptedMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("interrupted media action limit is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT
			action.transfer_id,
			action.id,
			action.target_id,
			target.seller_id,
			live_auth.id,
			live_auth.revision,
			plan.plan_digest,
			plan.target_set_root
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
		WHERE action.kind = 'upload_media'
			AND action.state = 'dispatching'
			AND attempt.delivery_state = 'not_dispatched'
			AND attempt.finished_at IS NULL
		ORDER BY action.id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list interrupted publication media actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.MediaActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan interrupted publication media action: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted publication media actions: %w", err)
	}
	return result, nil
}

// ListPendingMediaActions возвращает ожидающие медиа-действия для исполнения.
func (repository *Repository) ListPendingMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("pending media action limit is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		WITH pending AS (
			SELECT
				action.transfer_id,
				action.id AS action_id,
				action.target_id,
				target.seller_id,
				live_auth.id AS authorization_id,
				live_auth.revision AS authorization_revision,
				plan.plan_digest,
				plan.target_set_root,
				target.position AS target_position,
				ROW_NUMBER() OVER (
					PARTITION BY action.transfer_id, action.target_id
					ORDER BY action.id
				) AS lane_position
			FROM wb.publication_actions AS action
			JOIN wb.publication_plans AS plan
			  ON plan.transfer_id = action.transfer_id
			 AND plan.id = action.plan_id
			JOIN wb.transfers AS transfer
			  ON transfer.id = action.transfer_id
			JOIN wb.transfer_targets AS target
			  ON target.transfer_id = action.transfer_id
			 AND target.id = action.target_id
			JOIN LATERAL (
				SELECT live.id, live.revision
				FROM wb.transfer_live_authorizations AS live
				WHERE live.transfer_id = action.transfer_id
				  AND live.plan_id = action.plan_id
				  AND live.plan_digest = plan.plan_digest
				ORDER BY live.id DESC
				LIMIT 1
			) AS live_auth ON TRUE
			JOIN wb.publication_action_members AS media_member
			  ON media_member.transfer_id = action.transfer_id
			 AND media_member.action_id = action.id
			JOIN wb.product_identities AS identity
			  ON identity.seller_id = target.seller_id
			 AND identity.vendor_code_key = media_member.vendor_code
			WHERE action.kind = 'upload_media'
				AND action.state = 'planned'
				AND action.authorization_id IS NULL
				AND media_member.outcome_class IS NULL
				AND plan.state = 'executing'
				AND transfer.phase IN ('reconciling', 'media')
				AND transfer.outcome = 'running'
				AND identity.state = 'remote_present'
				AND identity.nm_id IS NOT NULL
				AND identity.active_action_id IS NULL
				AND (
					EXISTS (
						SELECT 1
						FROM wb.publication_action_members AS product_member
						JOIN wb.publication_actions AS product_action
						  ON product_action.transfer_id = product_member.transfer_id
						 AND product_action.id = product_member.action_id
						JOIN wb.publication_attributions AS attribution
						  ON attribution.transfer_id = product_member.transfer_id
						 AND attribution.action_member_id = product_member.id
						 AND attribution.level IN ('direct', 'observed_after_attempt')
						WHERE product_member.transfer_id = media_member.transfer_id
						  AND product_member.transfer_item_target_id
						      = media_member.transfer_item_target_id
						  AND product_action.plan_id = action.plan_id
						  AND attribution.plan_digest = plan.plan_digest
						  AND attribution.seller_id = target.seller_id
						  AND attribution.group_target_id = media_member.group_target_id
						  AND (attribution.nm_id IS NULL OR identity.nm_id = attribution.nm_id)
						  AND product_action.kind IN ('create_group', 'add_to_group')
						  AND product_action.state = 'terminal'
					)
					OR NOT EXISTS (
						SELECT 1
						FROM wb.publication_action_members AS product_member
						WHERE product_member.transfer_id = media_member.transfer_id
						  AND product_member.transfer_item_target_id
						      = media_member.transfer_item_target_id
					)
				)
		)
		SELECT
			transfer_id, action_id, target_id, seller_id,
			authorization_id, authorization_revision, plan_digest, target_set_root
		FROM pending
		WHERE lane_position = 1
		ORDER BY target_position, action_id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending publication media actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.MediaActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pending publication media action: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending publication media actions: %w", err)
	}
	return result, nil
}
