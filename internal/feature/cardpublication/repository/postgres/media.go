package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *Repository) ListDispatchableMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("dispatchable media action limit is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		WITH dispatchable AS (
			SELECT
				action.transfer_id,
				action.id AS action_id,
				action.target_id,
				target.cabinet_id,
				live_auth.id AS authorization_id,
				live_auth.revision AS authorization_revision,
				plan.plan_digest,
				plan.target_set_root,
				target.position AS target_position
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
			JOIN wb.publication_action_members AS product_member
			  ON product_member.transfer_id = media_member.transfer_id
			 AND product_member.transfer_item_target_id
			     = media_member.transfer_item_target_id
			JOIN wb.publication_actions AS product_action
			  ON product_action.transfer_id = product_member.transfer_id
			 AND product_action.id = product_member.action_id
			 AND product_action.plan_id = action.plan_id
			JOIN wb.publication_attributions AS attribution
			  ON attribution.transfer_id = product_member.transfer_id
			 AND attribution.action_member_id = product_member.id
			 AND attribution.level IN ('direct', 'observed_after_attempt')
			 AND attribution.plan_digest = plan.plan_digest
			 AND attribution.cabinet_id = target.cabinet_id
			 AND attribution.group_target_id = media_member.group_target_id
			JOIN wb.product_identities AS identity
			  ON identity.cabinet_id = target.cabinet_id
			 AND identity.vendor_code_key = media_member.vendor_code
			 AND identity.state = 'remote_present'
			 AND identity.nm_id = attribution.nm_id
			 AND identity.active_action_id IS NULL
			WHERE action.kind = 'upload_media'
				AND action.state = 'planned'
				AND action.authorization_id IS NULL
				AND media_member.outcome_class IS NULL
				AND product_action.kind IN ('create_group', 'add_to_group')
				AND product_action.state = 'terminal'
				AND plan.state = 'executing'
				AND transfer.phase IN ('reconciling', 'media')
				AND transfer.outcome = 'running'
				AND live_auth.state = 'authorized'
				AND live_auth.expires_at > CURRENT_TIMESTAMP
				AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_attempts AS attempt
					WHERE attempt.action_id = action.id
				)
				AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_actions AS unfinished_product
					WHERE unfinished_product.transfer_id = action.transfer_id
					  AND unfinished_product.plan_id = action.plan_id
					  AND unfinished_product.kind IN ('create_group', 'add_to_group')
					  AND unfinished_product.state NOT IN ('terminal', 'superseded')
				)
		)
		SELECT
			transfer_id, action_id, target_id, cabinet_id,
			authorization_id, authorization_revision, plan_digest, target_set_root
		FROM dispatchable
		ORDER BY target_position, action_id
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

func (repository *Repository) ListInterruptedMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("interrupted media action limit is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		SELECT
			action.transfer_id,
			action.id,
			action.target_id,
			target.cabinet_id,
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

func (repository *Repository) ListPendingMediaActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.MediaActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("pending media action limit is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		WITH pending AS (
			SELECT
				action.transfer_id,
				action.id AS action_id,
				action.target_id,
				target.cabinet_id,
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
			WHERE action.kind = 'upload_media'
				AND action.state = 'planned'
				AND action.authorization_id IS NULL
				AND media_member.outcome_class IS NULL
				AND plan.state = 'executing'
				AND transfer.phase IN ('reconciling', 'media')
				AND transfer.outcome = 'running'
				AND EXISTS (
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
					  AND attribution.cabinet_id = target.cabinet_id
					  AND attribution.group_target_id = media_member.group_target_id
					  AND product_action.kind IN ('create_group', 'add_to_group')
					  AND product_action.state = 'terminal'
				)
				AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_actions AS unfinished_product
					WHERE unfinished_product.transfer_id = action.transfer_id
					  AND unfinished_product.plan_id = action.plan_id
					  AND unfinished_product.kind IN ('create_group', 'add_to_group')
					  AND unfinished_product.state NOT IN ('terminal', 'superseded')
				)
		)
		SELECT
			transfer_id, action_id, target_id, cabinet_id,
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

func (repository *Repository) LockMediaAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	candidate cardpublication_service.MediaActionCandidate,
) (cardpublication_service.MediaAction, error) {
	if tx == nil || candidate.TransferID <= 0 || candidate.ActionID <= 0 ||
		candidate.AuthorizationID <= 0 {
		return cardpublication_service.MediaAction{},
			errors.New("lock publication media action: identity is invalid")
	}
	const query = `
		SELECT
			action.plan_id,
			action.revision,
			action.state,
			action.request_digest,
			action.request_payload,
			action.member_set_digest,
			action.media_link_set_root,
			target.seller_key,
			target.client_generation,
			target.credential_expires_at,
			(
				SELECT observation.id
				FROM wb.publication_observations AS observation
				WHERE observation.transfer_id = action.transfer_id
				  AND observation.target_id = action.target_id
				  AND observation.kind = 'normal_trash_preflight'
				  AND observation.created_at <= action.created_at
				ORDER BY observation.id DESC
				LIMIT 1
			),
			media_member.id,
			media_member.group_target_id,
			media_member.transfer_item_target_id,
			media_member.request_member_index,
			media_member.vendor_code,
			attribution.id,
			attribution.nm_id,
			identity.revision,
			attempt.id,
			attempt.recheck_observation_id,
			attempt.request_digest,
			attempt.request_payload,
			attempt.started_at,
			live_auth.revision,
			(
				SELECT COUNT(*)
				FROM wb.publication_action_members AS counted_member
				WHERE counted_member.transfer_id = action.transfer_id
				  AND counted_member.action_id = action.id
			),
			(
				SELECT COUNT(*)
				FROM wb.publication_action_members AS counted_product_member
				JOIN wb.publication_actions AS counted_product_action
				  ON counted_product_action.transfer_id
				     = counted_product_member.transfer_id
				 AND counted_product_action.id = counted_product_member.action_id
				JOIN wb.publication_attributions AS counted_attribution
				  ON counted_attribution.transfer_id
				     = counted_product_member.transfer_id
				 AND counted_attribution.action_member_id = counted_product_member.id
				 AND counted_attribution.level IN ('direct', 'observed_after_attempt')
				WHERE counted_product_member.transfer_id = media_member.transfer_id
				  AND counted_product_member.transfer_item_target_id
				      = media_member.transfer_item_target_id
				  AND counted_product_action.plan_id = action.plan_id
				  AND counted_attribution.plan_digest = plan.plan_digest
				  AND counted_attribution.cabinet_id = target.cabinet_id
				  AND counted_attribution.group_target_id = media_member.group_target_id
			)
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.id = $5
		JOIN wb.publication_action_members AS media_member
		  ON media_member.transfer_id = action.transfer_id
		 AND media_member.action_id = action.id
		JOIN wb.publication_action_members AS product_member
		  ON product_member.transfer_id = media_member.transfer_id
		 AND product_member.transfer_item_target_id
		     = media_member.transfer_item_target_id
		JOIN wb.publication_actions AS product_action
		  ON product_action.transfer_id = product_member.transfer_id
		 AND product_action.id = product_member.action_id
		 AND product_action.plan_id = action.plan_id
		JOIN wb.publication_attributions AS attribution
		  ON attribution.transfer_id = product_member.transfer_id
		 AND attribution.action_member_id = product_member.id
		 AND attribution.level IN ('direct', 'observed_after_attempt')
		 AND attribution.plan_digest = plan.plan_digest
		 AND attribution.cabinet_id = target.cabinet_id
		 AND attribution.group_target_id = media_member.group_target_id
		JOIN wb.product_identities AS identity
		  ON identity.cabinet_id = target.cabinet_id
		 AND identity.vendor_code_key = media_member.vendor_code
		 AND identity.nm_id = attribution.nm_id
		LEFT JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.target_id = $3
			AND target.cabinet_id = $4
			AND action.kind = 'upload_media'
			AND product_action.kind IN ('create_group', 'add_to_group')
			AND product_action.state = 'terminal'
			AND plan.plan_digest = $6
			AND plan.target_set_root = $7
			AND identity.state = 'remote_present'
			AND (
				(action.state = 'planned'
				 AND action.authorization_id IS NULL
				 AND identity.active_action_id IS NULL)
				OR
				(action.state = 'dispatching'
				 AND action.authorization_id = live_auth.id
				 AND identity.active_transfer_id = action.transfer_id
				 AND identity.active_action_id = action.id)
			)
		FOR UPDATE OF action, plan, media_member, identity;
	`
	action := cardpublication_service.MediaAction{ProductActionCandidate: candidate}
	var (
		requestDigest, memberDigest, mediaRoot []byte
		sellerKey, generation                  []byte
		preflightID                            pgtype.Int8
		attemptID, recheckID                   pgtype.Int8
		attemptRequestDigest                   []byte
		attemptRequestPayload                  []byte
		attemptStartedAt                       pgtype.Timestamptz
		currentAuthorizationRevision           int64
		memberCount, attributionCount          int64
	)
	err := tx.QueryRow(
		ctx,
		query,
		candidate.TransferID,
		candidate.ActionID,
		candidate.TargetID,
		candidate.CabinetID,
		candidate.AuthorizationID,
		candidate.PlanDigest[:],
		candidate.TargetSetRoot[:],
	).Scan(
		&action.PlanID,
		&action.Revision,
		&action.State,
		&requestDigest,
		&action.RequestPayload,
		&memberDigest,
		&mediaRoot,
		&sellerKey,
		&generation,
		&action.CredentialExpiresAt,
		&preflightID,
		&action.Member.ID,
		&action.Member.GroupTargetID,
		&action.Member.TransferItemTargetID,
		&action.Member.RequestMemberIndex,
		&action.Member.VendorCode,
		&action.AttributionID,
		&action.NMID,
		&action.IdentityRevision,
		&attemptID,
		&recheckID,
		&attemptRequestDigest,
		&attemptRequestPayload,
		&attemptStartedAt,
		&currentAuthorizationRevision,
		&memberCount,
		&attributionCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_service.MediaAction{},
			cardpublication_service.ErrMediaActionConflict
	}
	if err != nil {
		return cardpublication_service.MediaAction{},
			fmt.Errorf("lock publication media action: %w", err)
	}
	if len(requestDigest) != len(action.RequestDigest) ||
		len(memberDigest) != len(action.MemberSetDigest) ||
		len(mediaRoot) != len(action.MediaLinkSetRoot) ||
		len(sellerKey) != len(action.SellerKey) ||
		len(generation) != len(action.ClientGeneration) || !preflightID.Valid ||
		memberCount != 1 || attributionCount != 1 {
		return cardpublication_service.MediaAction{},
			cardpublication_service.ErrMediaActionConflict
	}
	copy(action.RequestDigest[:], requestDigest)
	copy(action.MemberSetDigest[:], memberDigest)
	copy(action.MediaLinkSetRoot[:], mediaRoot)
	copy(action.SellerKey[:], sellerKey)
	copy(action.ClientGeneration[:], generation)
	action.PreflightObservationID = preflightID.Int64
	action.AuthorizationRevision = currentAuthorizationRevision
	if attemptID.Valid {
		action.AttemptID = attemptID.Int64
	}
	if recheckID.Valid {
		action.RecheckObservationID = recheckID.Int64
	}
	if len(attemptRequestDigest) > 0 {
		if len(attemptRequestDigest) != len(action.AttemptRequestDigest) {
			return cardpublication_service.MediaAction{},
				cardpublication_service.ErrMediaActionConflict
		}
		copy(action.AttemptRequestDigest[:], attemptRequestDigest)
	}
	if len(attemptRequestPayload) > 0 {
		action.AttemptRequestPayload = append([]byte(nil), attemptRequestPayload...)
	}
	if attemptStartedAt.Valid {
		action.AttemptStartedAt = attemptStartedAt.Time.UTC()
	}
	if err := action.Validate(); err != nil {
		return cardpublication_service.MediaAction{}, err
	}
	return action, nil
}

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
			AND state = 'executing';
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
		action.AttributionID,
	).Scan(&attempt.ID, &attempt.StartedAt); err != nil {
		return cardpublication_service.MediaAttempt{},
			fmt.Errorf("insert publication media attempt: %w", err)
	}
	return attempt, nil
}

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

func (repository *Repository) RecordMediaResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.MediaAction,
	attempt cardpublication_service.MediaAttempt,
	mediaResult cardpublication_service.MediaMutationResult,
) (cardpublication_service.MediaGroupResult, error) {
	if tx == nil || action.State != "dispatching" || action.AttemptID != attempt.ID ||
		attempt.ActionID != action.ActionID || attempt.TransferID != action.TransferID ||
		attempt.AuthorizationID != action.AuthorizationID ||
		attempt.AttributionID != action.AttributionID ||
		attempt.RequestDigest != action.AttemptRequestDigest ||
		!equalBytes(attempt.RequestPayload, action.AttemptRequestPayload) {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
	}
	if err := mediaResult.Validate(); err != nil {
		return cardpublication_service.MediaGroupResult{}, err
	}
	const lockAttempt = `
		SELECT request_digest, request_payload, attribution_id
		FROM wb.publication_attempts
		WHERE transfer_id = $1
			AND id = $2
			AND action_id = $3
			AND authorization_id = $4
			AND finished_at IS NULL
		FOR UPDATE;
	`
	var storedDigest, storedPayload []byte
	var storedAttributionID int64
	if err := tx.QueryRow(
		ctx,
		lockAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
	).Scan(&storedDigest, &storedPayload, &storedAttributionID); err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("lock publication media attempt result: %w", err)
	}
	if !equalBytes(storedDigest, attempt.RequestDigest[:]) ||
		!equalBytes(storedPayload, attempt.RequestPayload) ||
		storedAttributionID != action.AttributionID {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
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
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("finish publication media attempt: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
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
	result, err = tx.Exec(
		ctx,
		finishMember,
		action.TransferID,
		action.ActionID,
		action.Member.ID,
		mediaResult.OutcomeClass,
		mediaResult.OutcomeCode,
		action.NMID,
	)
	if err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("finish publication media member: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
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
			AND state = 'dispatching';
	`
	result, err = tx.Exec(
		ctx,
		finishAction,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		mediaResult.OutcomeClass,
		mediaResult.OutcomeCode,
	)
	if err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("finish publication media action: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
	}
	const releaseIdentity = `
		UPDATE wb.product_identities
		SET active_transfer_id = NULL,
		    active_action_id = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE cabinet_id = $1
			AND vendor_code_key = $2
			AND state = 'remote_present'
			AND nm_id = $5
			AND active_transfer_id = $3
			AND active_action_id = $4;
	`
	result, err = tx.Exec(
		ctx,
		releaseIdentity,
		action.CabinetID,
		action.Member.VendorCode,
		action.TransferID,
		action.ActionID,
		action.NMID,
	)
	if err != nil {
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("release publication media identity: %w", err)
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

func loadMediaGroupResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.MediaAction,
) (cardpublication_service.MediaGroupResult, error) {
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
		return cardpublication_service.MediaGroupResult{},
			fmt.Errorf("load publication media group result: %w", err)
	}
	result := cardpublication_service.MediaGroupResult{
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
		return cardpublication_service.MediaGroupResult{},
			cardpublication_service.ErrMediaActionConflict
	}
	if err := result.Validate(); err != nil {
		return cardpublication_service.MediaGroupResult{}, err
	}
	return result, nil
}

var _ cardpublication_service.MediaJournalRepository = (*Repository)(nil)
