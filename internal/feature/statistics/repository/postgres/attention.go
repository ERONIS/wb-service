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
