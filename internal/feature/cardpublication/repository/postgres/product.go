package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *Repository) ListDispatchableProductActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.ProductActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("dispatchable product action limit is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		WITH dispatchable AS (
		SELECT
			action.transfer_id AS transfer_id,
			action.id AS action_id,
			action.target_id AS target_id,
			target.cabinet_id AS cabinet_id,
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
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.plan_id = action.plan_id
		 AND live_auth.plan_digest = plan.plan_digest
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'planned'
			AND action.authorization_id IS NULL
			AND plan.state IN ('awaiting_authorization', 'executing')
			AND transfer.phase IN (
				'awaiting_authorization', 'publishing', 'reconciling'
			)
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
				FROM wb.publication_actions AS active_action
				WHERE active_action.transfer_id = action.transfer_id
				  AND active_action.target_id = action.target_id
				  AND active_action.id <> action.id
				  AND active_action.kind IN ('create_group', 'add_to_group')
				  AND active_action.state IN ('dispatching', 'reconciling')
			)
		)
		SELECT
			transfer_id,
			action_id,
			target_id,
			cabinet_id,
			authorization_id,
			authorization_revision,
			plan_digest,
			target_set_root
		FROM dispatchable
		WHERE lane_position = 1
		ORDER BY target_position, action_id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list dispatchable product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dispatchable product actions: %w", err)
	}
	return result, nil
}

func (repository *Repository) ListInterruptedProductActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.ProductActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("interrupted product action limit is invalid")
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
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'dispatching'
			AND attempt.delivery_state = 'not_dispatched'
			AND attempt.finished_at IS NULL
		ORDER BY action.id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list interrupted product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted product actions: %w", err)
	}
	return result, nil
}

func (repository *Repository) LockProductAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	candidate cardpublication_service.ProductActionCandidate,
) (cardpublication_service.ProductAction, error) {
	if tx == nil || candidate.TransferID <= 0 || candidate.ActionID <= 0 ||
		candidate.AuthorizationID <= 0 {
		return cardpublication_service.ProductAction{}, errors.New(
			"lock publication product action: identity is invalid",
		)
	}
	const query = `
		SELECT
			action.plan_id,
			action.revision,
			action.kind,
			action.state,
			action.request_digest,
			action.request_payload,
			action.member_set_digest,
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
			attempt.id,
			attempt.recheck_observation_id,
			attempt.started_at,
			live_auth.revision
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
		LEFT JOIN wb.publication_attempts AS attempt
		  ON attempt.action_id = action.id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.target_id = $3
			AND target.cabinet_id = $4
			AND plan.plan_digest = $6
			AND plan.target_set_root = $7
		FOR UPDATE OF action, plan;
	`
	action := cardpublication_service.ProductAction{ProductActionCandidate: candidate}
	var (
		requestDigest, memberDigest  []byte
		sellerKey, generation        []byte
		preflightID                  pgtype.Int8
		attemptID, recheckID         pgtype.Int8
		attemptStartedAt             pgtype.Timestamptz
		currentAuthorizationRevision int64
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
		&action.Kind,
		&action.State,
		&requestDigest,
		&action.RequestPayload,
		&memberDigest,
		&sellerKey,
		&generation,
		&action.CredentialExpiresAt,
		&preflightID,
		&attemptID,
		&recheckID,
		&attemptStartedAt,
		&currentAuthorizationRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_service.ProductAction{}, cardpublication_service.ErrProductActionConflict
	}
	if err != nil {
		return cardpublication_service.ProductAction{}, fmt.Errorf(
			"lock publication product action: %w",
			err,
		)
	}
	if len(requestDigest) != len(action.RequestDigest) ||
		len(memberDigest) != len(action.MemberSetDigest) ||
		len(sellerKey) != len(action.SellerKey) ||
		len(generation) != len(action.ClientGeneration) || !preflightID.Valid {
		return cardpublication_service.ProductAction{}, cardpublication_service.ErrProductActionConflict
	}
	copy(action.RequestDigest[:], requestDigest)
	copy(action.MemberSetDigest[:], memberDigest)
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
	if attemptStartedAt.Valid {
		action.AttemptStartedAt = attemptStartedAt.Time.UTC()
	}

	const membersQuery = `
		SELECT
			id,
			group_target_id,
			transfer_item_target_id,
			request_member_index,
			vendor_code
		FROM wb.publication_action_members
		WHERE transfer_id = $1 AND action_id = $2
		ORDER BY request_member_index
		FOR UPDATE;
	`
	rows, err := tx.Query(ctx, membersQuery, candidate.TransferID, candidate.ActionID)
	if err != nil {
		return cardpublication_service.ProductAction{}, fmt.Errorf(
			"lock publication product action members: %w",
			err,
		)
	}
	defer rows.Close()
	for rows.Next() {
		var member cardpublication_service.ProductActionMember
		if err := rows.Scan(
			&member.ID,
			&member.GroupTargetID,
			&member.TransferItemTargetID,
			&member.RequestMemberIndex,
			&member.VendorCode,
		); err != nil {
			return cardpublication_service.ProductAction{}, fmt.Errorf(
				"scan publication product action member: %w",
				err,
			)
		}
		action.Members = append(action.Members, member)
	}
	if err := rows.Err(); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	if err := lockProductIdentities(ctx, tx, action); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	if err := action.Validate(); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	return action, nil
}

func (repository *Repository) InsertActionObservation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	kind string,
	observation cardpublication_service.ObservationDraft,
) (int64, error) {
	if tx == nil || transferID <= 0 ||
		(kind != "targeted_recheck" && kind != "post_submission") {
		return 0, errors.New("insert action observation command is invalid")
	}
	const insert = `
		INSERT INTO wb.publication_observations (
			transfer_id, target_id, cabinet_id, kind, observation_digest,
			normal_count, trash_count, snapshot, observed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (
			transfer_id, target_id, kind, observation_digest
		) DO NOTHING
		RETURNING id;
	`
	var observationID int64
	err := tx.QueryRow(
		ctx,
		insert,
		transferID,
		observation.TargetID,
		observation.CabinetID,
		kind,
		observation.Digest[:],
		observation.NormalCount,
		observation.TrashCount,
		observation.Payload,
		observation.ObservedAt,
	).Scan(&observationID)
	if errors.Is(err, pgx.ErrNoRows) {
		const existing = `
			SELECT id
			FROM wb.publication_observations
			WHERE transfer_id = $1
				AND target_id = $2
				AND kind = $3
				AND observation_digest = $4;
		`
		err = tx.QueryRow(
			ctx,
			existing,
			transferID,
			observation.TargetID,
			kind,
			observation.Digest[:],
		).Scan(&observationID)
	}
	if err != nil {
		return 0, fmt.Errorf("insert publication action observation: %w", err)
	}
	return observationID, nil
}

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

func (repository *Repository) BeginProductAttempt(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_service.BeginProductAttemptCommand,
) (cardpublication_service.ProductAttempt, error) {
	action := command.Action
	if tx == nil || action.State != "planned" || action.AttemptID != 0 ||
		command.Authorization.AuthorizationID != action.AuthorizationID ||
		command.Authorization.Revision != action.AuthorizationRevision ||
		command.Baseline.Validate() != nil ||
		command.Baseline.TransferID != action.TransferID ||
		command.Baseline.ActionID != action.ActionID ||
		command.Baseline.CabinetID != action.CabinetID ||
		command.RecheckObservationID <= 0 {
		return cardpublication_service.ProductAttempt{}, errors.New(
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
		WHERE cabinet_id = $1
			AND vendor_code_key = ANY($2::text[])
			AND state = 'remote_missing'
			AND active_action_id IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		bindIdentities,
		action.CabinetID,
		action.VendorCodes(),
		action.TransferID,
		action.ActionID,
	)
	if err != nil {
		return cardpublication_service.ProductAttempt{}, fmt.Errorf(
			"bind publication product identities: %w",
			err,
		)
	}
	if result.RowsAffected() != int64(len(action.Members)) {
		return cardpublication_service.ProductAttempt{}, cardpublication_service.ErrProductActionConflict
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
			AND action.authorization_id IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_actions AS active_action
				WHERE active_action.transfer_id = action.transfer_id
				  AND active_action.target_id = action.target_id
				  AND active_action.id <> action.id
				  AND active_action.kind IN ('create_group', 'add_to_group')
				  AND active_action.state IN ('dispatching', 'reconciling')
			);
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
		return cardpublication_service.ProductAttempt{}, fmt.Errorf(
			"begin publication product action: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ProductAttempt{}, cardpublication_service.ErrProductActionConflict
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
		return cardpublication_service.ProductAttempt{}, fmt.Errorf(
			"begin publication plan execution: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ProductAttempt{}, cardpublication_service.ErrProductActionConflict
	}
	const insertAttempt = `
		INSERT INTO wb.publication_attempts (
			transfer_id,
			action_id,
			authorization_id,
			recheck_observation_id,
			baseline_cabinet_id,
			baseline_cursor_revision,
			baseline_cursor_updated_at,
			baseline_cursor_batch_uuid,
			baseline_captured_at,
			request_digest,
			request_payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, started_at;
	`
	attempt := cardpublication_service.ProductAttempt{
		TransferID:           action.TransferID,
		ActionID:             action.ActionID,
		AuthorizationID:      action.AuthorizationID,
		RecheckObservationID: command.RecheckObservationID,
		RequestDigest:        action.RequestDigest,
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
		action.RequestDigest[:],
		action.RequestPayload,
	).Scan(&attempt.ID, &attempt.StartedAt); err != nil {
		return cardpublication_service.ProductAttempt{}, fmt.Errorf(
			"insert publication product attempt: %w",
			err,
		)
	}
	return attempt, nil
}

func (repository *Repository) RecordSubmission(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_service.RecordSubmissionCommand,
) error {
	if tx == nil {
		return errors.New("record product submission: DBTX is nil")
	}
	if err := cardpublication_service.ValidateSubmissionResult(command.Result); err != nil {
		return err
	}
	action, attempt, result := command.Action, command.Attempt, command.Result
	if action.State != "dispatching" || action.AttemptID != attempt.ID ||
		attempt.ActionID != action.ActionID || attempt.TransferID != action.TransferID ||
		attempt.AuthorizationID != action.AuthorizationID ||
		attempt.RequestDigest != action.RequestDigest {
		return cardpublication_service.ErrProductActionConflict
	}
	const lockAttempt = `
		SELECT request_digest
		FROM wb.publication_attempts
		WHERE transfer_id = $1
			AND id = $2
			AND action_id = $3
			AND authorization_id = $4
			AND finished_at IS NULL
		FOR UPDATE;
	`
	var storedDigest []byte
	if err := tx.QueryRow(
		ctx,
		lockAttempt,
		action.TransferID,
		attempt.ID,
		action.ActionID,
		action.AuthorizationID,
	).Scan(&storedDigest); err != nil {
		return fmt.Errorf("lock publication product attempt result: %w", err)
	}
	if len(storedDigest) != len(action.RequestDigest) ||
		!equalBytes(storedDigest, action.RequestDigest[:]) {
		return cardpublication_service.ErrProductActionConflict
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
		return cardpublication_service.ErrProductActionConflict
	}

	if result.Disposition == cardpublication_service.SubmissionAccepted ||
		result.Disposition == cardpublication_service.SubmissionUncertain {
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
			return cardpublication_service.ErrProductActionConflict
		}
		if result.Disposition == cardpublication_service.SubmissionUncertain {
			const blockIdentities = `
				UPDATE wb.product_identities
				SET state = 'blocked_uncertain',
				    revision = revision + 1,
				    updated_at = CURRENT_TIMESTAMP
				WHERE cabinet_id = $1
					AND vendor_code_key = ANY($2::text[])
					AND active_transfer_id = $3
					AND active_action_id = $4
					AND state = 'mutation_pending';
			`
			if _, err := tx.Exec(
				ctx,
				blockIdentities,
				action.CabinetID,
				action.VendorCodes(),
				action.TransferID,
				action.ActionID,
			); err != nil {
				return fmt.Errorf("block uncertain publication identities: %w", err)
			}
		}
		return nil
	}

	memberResults := make(map[int64]cardpublication_service.MemberSubmissionResult)
	for _, memberResult := range result.MemberResults {
		memberResults[memberResult.ActionMemberID] = memberResult
	}
	if len(memberResults) != len(action.Members) {
		return cardpublication_service.ErrProductActionConflict
	}
	actionClass := transfer_service.ResultRejected
	for _, member := range action.Members {
		memberResult, exists := memberResults[member.ID]
		if !exists {
			return cardpublication_service.ErrProductActionConflict
		}
		if memberResult.OutcomeClass == transfer_service.ResultInternalError {
			actionClass = transfer_service.ResultInternalError
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
			return cardpublication_service.ErrProductActionConflict
		}
		identityState := "rejected"
		clearActive := true
		if memberResult.OutcomeClass == transfer_service.ResultInternalError {
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
			WHERE cabinet_id = $1
				AND vendor_code_key = $2
				AND active_transfer_id = $3
				AND active_action_id = $4;
		`
		update, err = tx.Exec(
			ctx,
			finishIdentity,
			action.CabinetID,
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
			return cardpublication_service.ErrProductActionConflict
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
		return cardpublication_service.ErrProductActionConflict
	}
	return reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID)
}

func (repository *Repository) ListReconcilingProductActions(
	ctx context.Context,
	olderThan time.Time,
	limit int,
) ([]cardpublication_service.ProductActionCandidate, error) {
	if olderThan.IsZero() || limit <= 0 || limit > 100 {
		return nil, errors.New("reconciling product action query is invalid")
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
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'reconciling'
			AND attempt.finished_at IS NOT NULL
			AND action.updated_at <= $1
		ORDER BY action.updated_at, action.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list reconciling product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
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

func (repository *Repository) LoadErrorBatchMatches(
	ctx context.Context,
	action cardpublication_service.ProductAction,
) ([]cardpublication_service.ErrorBatchMatch, error) {
	if action.AttemptID <= 0 {
		return nil, cardpublication_service.ErrProductActionConflict
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
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
		  ON batch.cabinet_id = attempt.baseline_cabinet_id
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
		action.VendorCodes(),
	)
	if err != nil {
		return nil, fmt.Errorf("load publication Error List matches: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ErrorBatchMatch, 0)
	for rows.Next() {
		var match cardpublication_service.ErrorBatchMatch
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

func (repository *Repository) RecordProductReconciliationPoll(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
	postObservationID int64,
) error {
	if tx == nil || action.State != "reconciling" || action.AttemptID <= 0 ||
		postObservationID <= 0 {
		return errors.New("record product reconciliation poll command is invalid")
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
		return cardpublication_service.ErrProductActionConflict
	}
	return nil
}

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

func (repository *Repository) FinishProductReconciliation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
	postObservationID int64,
	memberResults []cardpublication_service.MemberReconciliationResult,
	actionClass transfer_service.ResultClass,
	actionCode string,
) error {
	if tx == nil || action.State != "reconciling" || action.AttemptID <= 0 ||
		postObservationID <= 0 || !actionClass.IsValid() || actionCode == "" ||
		len(actionCode) > 128 {
		return errors.New("finish product reconciliation command is invalid")
	}
	if err := cardpublication_service.ValidateMemberReconciliationResults(
		action,
		memberResults,
	); err != nil {
		return err
	}
	results := make(map[int64]cardpublication_service.MemberReconciliationResult)
	for _, result := range memberResults {
		results[result.ActionMemberID] = result
	}
	for _, member := range action.Members {
		result, exists := results[member.ID]
		if !exists {
			return cardpublication_service.ErrProductActionConflict
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
			return cardpublication_service.ErrProductActionConflict
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
			WHERE cabinet_id = $1
				AND vendor_code_key = $2
				AND active_transfer_id = $3
				AND active_action_id = $4;
		`
		updated, err = tx.Exec(
			ctx,
			finishIdentity,
			action.CabinetID,
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
			return cardpublication_service.ErrProductActionConflict
		}

		if result.AttributionLevel == "observed_after_attempt" {
			const insertAttribution = `
				INSERT INTO wb.publication_attributions (
					transfer_id, group_target_id, action_id, action_member_id,
					cabinet_id, nm_id, plan_digest, attempt_id,
					preflight_observation_id, post_observation_id, level,
					safe_reason_code
				)
				VALUES (
					$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
					'observed_after_attempt', $11
				);
			`
			if _, err := tx.Exec(
				ctx,
				insertAttribution,
				action.TransferID,
				member.GroupTargetID,
				action.ActionID,
				member.ID,
				action.CabinetID,
				result.NMID,
				action.PlanDigest[:],
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
					AND batch.cabinet_id = attempt.baseline_cabinet_id
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
			AND state = 'reconciling';
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
		return cardpublication_service.ErrProductActionConflict
	}
	return reducePublicationPlan(ctx, tx, action.TransferID, action.PlanID)
}

func finishPotentialMediaActions(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
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
					  AND attribution.plan_digest = $5
					  AND attribution.cabinet_id = $6
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
		action.PlanDigest[:],
		action.CabinetID,
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

func (repository *Repository) PendingMediaGroupTargetIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
) ([]int64, error) {
	if tx == nil || action.TransferID <= 0 || action.PlanID <= 0 || len(action.Members) == 0 {
		return nil, cardpublication_service.ErrProductActionConflict
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

func reducePublicationPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	planID int64,
) error {
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
			AND plan.state = 'executing'
			AND action_counts.total > 0
			AND action_counts.terminal = action_counts.total;
	`
	if _, err := tx.Exec(ctx, reduce, transferID, planID); err != nil {
		return fmt.Errorf("reduce publication plan: %w", err)
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func nullablePositive(value int64) any {
	if value > 0 {
		return value
	}
	return nil
}

func lockProductIdentities(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
) error {
	const query = `
		SELECT vendor_code_key
		FROM wb.product_identities
		WHERE cabinet_id = $1
			AND vendor_code_key = ANY($2::text[])
		ORDER BY vendor_code_key
		FOR UPDATE;
	`
	rows, err := tx.Query(ctx, query, action.CabinetID, action.VendorCodes())
	if err != nil {
		return fmt.Errorf("lock publication product identities: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ignored string
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(action.Members) {
		return cardpublication_service.ErrProductActionConflict
	}
	return nil
}

func scanProductActionCandidate(row interface{ Scan(...any) error }) (
	cardpublication_service.ProductActionCandidate,
	error,
) {
	var candidate cardpublication_service.ProductActionCandidate
	var transferID, authorizationID int64
	var planDigest, targetSetRoot []byte
	if err := row.Scan(
		&transferID,
		&candidate.ActionID,
		&candidate.TargetID,
		&candidate.CabinetID,
		&authorizationID,
		&candidate.AuthorizationRevision,
		&planDigest,
		&targetSetRoot,
	); err != nil {
		return cardpublication_service.ProductActionCandidate{}, err
	}
	if len(planDigest) != len(candidate.PlanDigest) ||
		len(targetSetRoot) != len(candidate.TargetSetRoot) {
		return cardpublication_service.ProductActionCandidate{},
			errors.New("publication product candidate digest is invalid")
	}
	candidate.TransferID = transfer_service.TransferID(transferID)
	candidate.AuthorizationID = transfer_service.LiveAuthorizationID(authorizationID)
	copy(candidate.PlanDigest[:], planDigest)
	copy(candidate.TargetSetRoot[:], targetSetRoot)
	return candidate, nil
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
