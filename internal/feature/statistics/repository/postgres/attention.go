package statistics_postgres_repository

import (
	"context"
	"fmt"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
)

func (repository *Repository) ListAttention(
	ctx context.Context,
	filter statistics_service.AttentionFilter,
) ([]statistics_service.AttentionRow, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	var builder whereBuilder
	builder.addRaw("item.state = 'terminal'")
	builder.addRaw("item.outcome_class IN ('unresolved', 'internal_error')")
	builder.addRaw("item.attention_closed_at IS NULL")
	builder.addRaw("item.source_action_id IS NOT NULL")
	builder.addRaw("action.kind IN ('create_group', 'add_to_group')")
	if filter.TransferID > 0 {
		builder.add("item.transfer_id = $%d", filter.TransferID)
	}
	if filter.ItemTargetID > 0 {
		builder.add("item.item_target_id = $%d", filter.ItemTargetID)
	}
	builder.arguments = append(builder.arguments, filter.Limit, filter.Offset)
	query := `
		SELECT
			item.transfer_id, item.item_target_id, item.group_target_id,
			item.cabinet_id, item.vendor_code, item.outcome_class, item.outcome_code,
			action.id, member.id, action.revision, action.kind,
			COALESCE(attempt.id, 0),
			COALESCE(observation.id, 0),
			COALESCE(error_evidence.error_batch_id, 0),
			item.finished_at
		FROM wb.statistics_item_facts AS item
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = item.transfer_id
		 AND action.id = item.source_action_id
		JOIN wb.publication_action_members AS member
		  ON member.transfer_id = item.transfer_id
		 AND member.action_id = action.id
		 AND member.transfer_item_target_id = item.item_target_id
		LEFT JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		LEFT JOIN LATERAL (
			SELECT candidate.id
			FROM wb.publication_observations AS candidate
			WHERE candidate.transfer_id = action.transfer_id
			  AND candidate.target_id = action.target_id
			  AND candidate.kind = 'post_submission'
			  AND attempt.id IS NOT NULL
			  AND candidate.observed_at >= attempt.started_at
			ORDER BY candidate.observed_at DESC, candidate.id DESC
			LIMIT 1
		) AS observation ON true
		LEFT JOIN LATERAL (
			SELECT correlation.error_batch_id
			FROM wb.publication_error_correlations AS correlation
			WHERE correlation.transfer_id = item.transfer_id
			  AND correlation.action_id = action.id
			  AND correlation.action_member_id = member.id
			  AND (attempt.id IS NULL OR correlation.attempt_id = attempt.id)
			ORDER BY correlation.id DESC
			LIMIT 1
		) AS error_evidence ON true` + builder.clause() + `
		ORDER BY item.finished_at DESC, item.item_target_id DESC
		LIMIT $` + fmt.Sprint(len(builder.arguments)-1) + `
		OFFSET $` + fmt.Sprint(len(builder.arguments))
	rows, err := repository.pool.Query(ctx, query, builder.arguments...)
	if err != nil {
		return nil, fmt.Errorf("list statistics attention: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.AttentionRow, 0, filter.Limit)
	for rows.Next() {
		var item statistics_service.AttentionRow
		if err := rows.Scan(
			&item.TransferID, &item.ItemTargetID, &item.GroupTargetID,
			&item.CabinetID, &item.VendorCode, &item.OutcomeClass,
			&item.OutcomeCode, &item.ActionID, &item.ActionMemberID,
			&item.ActionRevision, &item.ActionKind, &item.AttemptID,
			&item.ObservationEvidenceID, &item.ErrorBatchEvidenceID,
			&item.FinishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan statistics attention: %w", err)
		}
		item.FinishedAt = item.FinishedAt.UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statistics attention: %w", err)
	}
	return result, nil
}

func (repository *Repository) ResolveVerifiedCard(
	ctx context.Context,
	itemTargetID int64,
	nmID int64,
	imtID int64,
	subjectID int64,
) error {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	const updateItem = `
		UPDATE wb.transfer_item_targets
		SET nm_id = $2,
		    outcome_class = 'success',
		    outcome_code = 'VERIFIED_IN_WB',
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`
	const updateActionMember = `
		UPDATE wb.publication_action_members
		SET nm_id = $2,
		    outcome_class = 'success',
		    outcome_code = 'VERIFIED_IN_WB',
		    finished_at = COALESCE(finished_at, CURRENT_TIMESTAMP),
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_item_target_id = $1;
	`
	const updateIdentity = `
		UPDATE wb.product_identities AS iden
		SET state = 'remote_present',
		    nm_id = $2::bigint,
		    imt_id = COALESCE(NULLIF($3::bigint, 0), iden.imt_id),
		    subject_id = COALESCE(NULLIF($4::bigint, 0), iden.subject_id),
		    active_transfer_id = NULL,
		    active_action_id = NULL,
		    revision = iden.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.transfer_item_targets AS it
		JOIN wb.transfer_items AS ti ON ti.transfer_id = it.transfer_id AND ti.id = it.transfer_item_id
		JOIN wb.transfer_group_targets AS tgt ON tgt.transfer_id = it.transfer_id AND tgt.id = it.group_target_id
		JOIN wb.transfer_targets AS tt ON tt.transfer_id = it.transfer_id AND tt.id = tgt.target_id
		WHERE it.id = $1
		  AND iden.cabinet_id = tt.cabinet_id
		  AND iden.vendor_code_key = ti.vendor_code;
	`
	const updateGroup = `
		WITH group_counts AS (
			SELECT
				group_target_id,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE outcome_class = 'success') AS succeeded,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'skipped') AS skipped,
				COUNT(*) FILTER (WHERE outcome_class IN ('unresolved', 'internal_error')) AS unresolved
			FROM wb.transfer_item_targets
			WHERE group_target_id = (SELECT group_target_id FROM wb.transfer_item_targets WHERE id = $1)
			GROUP BY group_target_id
		)
		UPDATE wb.transfer_group_targets AS tgt
		SET publication_status = CASE
				WHEN gc.unresolved = 0 AND gc.succeeded > 0 THEN 'succeeded'
				WHEN gc.unresolved = 0 AND gc.rejected = gc.total THEN 'rejected'
				ELSE tgt.publication_status
			END,
		    overall_outcome = CASE
				WHEN gc.unresolved = 0 AND gc.succeeded > 0 THEN 'success'
				WHEN gc.unresolved = 0 AND gc.rejected = gc.total THEN 'rejected'
				ELSE tgt.overall_outcome
			END,
		    attention_code = CASE
				WHEN gc.unresolved = 0 THEN NULL
				ELSE tgt.attention_code
			END,
		    finished_at = CASE
				WHEN gc.unresolved = 0 THEN COALESCE(tgt.finished_at, CURRENT_TIMESTAMP)
				ELSE tgt.finished_at
			END,
		    revision = tgt.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts gc
		WHERE tgt.id = gc.group_target_id;
	`
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for resolve verified card: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, updateItem, itemTargetID, nmID); err != nil {
		return fmt.Errorf("update transfer item target: %w", err)
	}
	if _, err := tx.Exec(ctx, updateActionMember, itemTargetID, nmID); err != nil {
		return fmt.Errorf("update publication action member: %w", err)
	}
	if _, err := tx.Exec(ctx, updateIdentity, itemTargetID, nmID, imtID, subjectID); err != nil {
		return fmt.Errorf("update product identity: %w", err)
	}
	if _, err := tx.Exec(ctx, updateGroup, itemTargetID); err != nil {
		return fmt.Errorf("update transfer group target: %w", err)
	}
	return tx.Commit(ctx)
}

func (repository *Repository) RequeueCardForCreation(
	ctx context.Context,
	itemTargetID int64,
) error {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	const resetItem = `
		UPDATE wb.transfer_item_targets
		SET state = 'running',
		    outcome_class = NULL,
		    outcome_code = NULL,
		    nm_id = NULL,
		    source_action_id = NULL,
		    attention_closed_at = NULL,
		    finished_at = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`
	const resetGroup = `
		UPDATE wb.transfer_group_targets
		SET publication_status = 'not_started',
		    media_status = 'not_started',
		    overall_outcome = 'running',
		    attention_code = NULL,
		    publication_plan_id = NULL,
		    finished_at = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT group_target_id FROM wb.transfer_item_targets WHERE id = $1);
	`
	const resetIdentity = `
		UPDATE wb.product_identities AS iden
		SET state = 'remote_missing',
		    nm_id = NULL,
		    active_transfer_id = NULL,
		    active_action_id = NULL,
		    revision = iden.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.transfer_item_targets AS it
		JOIN wb.transfer_items AS ti ON ti.transfer_id = it.transfer_id AND ti.id = it.transfer_item_id
		JOIN wb.transfer_group_targets AS tgt ON tgt.transfer_id = it.transfer_id AND tgt.id = it.group_target_id
		JOIN wb.transfer_targets AS tt ON tt.transfer_id = it.transfer_id AND tt.id = tgt.target_id
		WHERE it.id = $1
		  AND iden.cabinet_id = tt.cabinet_id
		  AND iden.vendor_code_key = ti.vendor_code;
	`
	const resumeTransfer = `
		UPDATE wb.transfers
		SET phase = 'publishing',
		    outcome = 'running',
		    attention_code = NULL,
		    finished_at = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT transfer_id FROM wb.transfer_item_targets WHERE id = $1)
		  AND phase = 'finished';
	`
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for requeue card for creation: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, resetItem, itemTargetID); err != nil {
		return fmt.Errorf("reset transfer item target: %w", err)
	}
	if _, err := tx.Exec(ctx, resetGroup, itemTargetID); err != nil {
		return fmt.Errorf("reset transfer group target: %w", err)
	}
	if _, err := tx.Exec(ctx, resetIdentity, itemTargetID); err != nil {
		return fmt.Errorf("reset product identity: %w", err)
	}
	if _, err := tx.Exec(ctx, resumeTransfer, itemTargetID); err != nil {
		return fmt.Errorf("resume transfer: %w", err)
	}
	return tx.Commit(ctx)
}


