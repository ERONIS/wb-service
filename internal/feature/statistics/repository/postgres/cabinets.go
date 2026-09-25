package statistics_postgres_repository

import (
	"context"
	"fmt"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	"github.com/jackc/pgx/v5/pgtype"
)

// Media belongs to an individual item. The group outcome must not make its
// successfully completed siblings look broken (or still uploading).
const cabinetTaskMediaJoin = `
		LEFT JOIN (
			SELECT DISTINCT ON (member.transfer_item_target_id)
			       member.transfer_id, member.transfer_item_target_id,
			       action.id, action.state, action.outcome_class, action.outcome_code
			FROM wb.publication_action_members AS member
			JOIN wb.publication_actions AS action
			  ON action.transfer_id = member.transfer_id AND action.id = member.action_id
			WHERE member.transfer_id = $1
			  AND action.kind = 'upload_media' AND action.state <> 'superseded'
			ORDER BY member.transfer_item_target_id, action.id DESC
		) AS media ON media.transfer_id = item.transfer_id
		          AND media.transfer_item_target_id = item.item_target_id
		LEFT JOIN LATERAL (
			SELECT CASE
				WHEN item.state = 'terminal' AND item.outcome_class NOT IN ('success', 'skipped')
					THEN item.outcome_class
				WHEN media.state = 'terminal' AND media.outcome_class = 'rejected' THEN 'partial'
				WHEN media.state = 'terminal' THEN media.outcome_class
				WHEN media.id IS NOT NULL THEN 'running'
				WHEN item.state = 'terminal' AND item.outcome_class IN ('success', 'skipped')
					THEN item.outcome_class
				ELSE group_target.overall_outcome
			END AS overall_outcome,
			CASE
				WHEN media.state = 'planned' THEN 'not_started'
				WHEN media.state IN ('dispatching', 'reconciling') THEN 'running'
				WHEN media.state = 'terminal' AND media.outcome_class = 'success' THEN 'succeeded'
				WHEN media.state = 'terminal' THEN media.outcome_class
				ELSE group_target.media_status
			END AS media_status
		) AS task ON true
`

func (repository *Repository) ListCabinetProgress(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.CabinetProgressRow, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	const query = `
		SELECT
			target.id,
			target.position,
			target.cabinet_id,
			COUNT(item.item_target_id),
			COUNT(item.item_target_id) FILTER (
				WHERE task.overall_outcome = 'running'
				  AND group_target.preparation_status <> 'running'
				  AND group_target.publication_status <> 'running'
				  AND task.media_status <> 'running'
			),
			COUNT(item.item_target_id) FILTER (
				WHERE task.overall_outcome = 'running'
				  AND (
					group_target.preparation_status = 'running'
					OR group_target.publication_status = 'running'
					OR task.media_status = 'running'
				  )
			),
			COUNT(item.item_target_id) FILTER (
				WHERE task.overall_outcome <> 'running'
			),
			COUNT(item.item_target_id) FILTER (
				WHERE task.overall_outcome IN ('success', 'skipped')
				  AND item.outcome_class IN ('success', 'skipped')
			),
			COUNT(item.item_target_id) FILTER (
				WHERE task.overall_outcome <> 'running'
				  AND (
					task.overall_outcome NOT IN ('success', 'skipped')
					OR item.outcome_class NOT IN ('success', 'skipped')
				  )
			),
			COUNT(item.item_target_id) FILTER (
				WHERE (media.id IS NULL AND group_target.attention_code IS NOT NULL
				       AND task.overall_outcome IN ('unresolved', 'internal_error'))
				   OR media.outcome_class IN ('unresolved', 'internal_error')
				   OR (
					item.outcome_class IN ('unresolved', 'internal_error')
					AND item.attention_closed_at IS NULL
				   )
			)
		FROM wb.transfer_targets AS target
		LEFT JOIN wb.statistics_item_facts AS item
		  ON item.transfer_id = target.transfer_id
		 AND item.target_id = target.id
		LEFT JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = item.transfer_id
		 AND group_target.id = item.group_target_id
		` + cabinetTaskMediaJoin + `
		WHERE target.transfer_id = $1
		GROUP BY target.id, target.position, target.cabinet_id
		ORDER BY target.position, target.id`

	rows, err := repository.pool.Query(ctx, query, transferID)
	if err != nil {
		return nil, fmt.Errorf("list statistics cabinet progress: %w", err)
	}
	defer rows.Close()

	result := make([]statistics_service.CabinetProgressRow, 0)
	for rows.Next() {
		var item statistics_service.CabinetProgressRow
		if err := rows.Scan(
			&item.TargetID,
			&item.TargetPosition,
			&item.CabinetID,
			&item.Total,
			&item.Pending,
			&item.Running,
			&item.Terminal,
			&item.Ready,
			&item.Errors,
			&item.Attention,
		); err != nil {
			return nil, fmt.Errorf("scan statistics cabinet progress: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statistics cabinet progress: %w", err)
	}
	return result, nil
}

func (repository *Repository) ListCabinetTasks(
	ctx context.Context,
	filter statistics_service.CabinetTaskFilter,
) (statistics_service.CabinetTaskPage, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	const countQuery = `
		SELECT COUNT(*)
		FROM wb.statistics_item_facts AS item
		WHERE item.transfer_id = $1
		  AND item.target_id = $2`
	var total int64
	if err := repository.pool.QueryRow(
		ctx,
		countQuery,
		filter.TransferID,
		filter.TargetID,
	).Scan(&total); err != nil {
		return statistics_service.CabinetTaskPage{},
			fmt.Errorf("count statistics cabinet tasks: %w", err)
	}

	const listQuery = `
		SELECT
			item.transfer_id,
			item.item_target_id,
			item.target_id,
			item.cabinet_id,
			item.item_position,
			item.vendor_code,
			source_group.source_group_name,
			item.state,
			COALESCE(item.outcome_class, ''),
			COALESCE(item.outcome_code, ''),
			COALESCE(item.nm_id, 0),
			group_target.preparation_status,
			group_target.publication_status,
			task.media_status,
			task.overall_outcome,
			COALESCE(group_target.attention_code, ''),
			COALESCE(media.state, ''),
			COALESCE(media.outcome_code, ''),
			item.created_at,
			item.started_at,
			item.finished_at
		FROM wb.statistics_item_facts AS item
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = item.transfer_id
		 AND group_target.id = item.group_target_id
		JOIN wb.transfer_groups AS source_group
		  ON source_group.transfer_id = group_target.transfer_id
		 AND source_group.id = group_target.source_group_id
		` + cabinetTaskMediaJoin + `
		WHERE item.transfer_id = $1
		  AND item.target_id = $2
		ORDER BY item.item_position, item.item_target_id
		LIMIT $3 OFFSET $4`
	rows, err := repository.pool.Query(
		ctx,
		listQuery,
		filter.TransferID,
		filter.TargetID,
		filter.Limit,
		filter.Offset,
	)
	if err != nil {
		return statistics_service.CabinetTaskPage{},
			fmt.Errorf("list statistics cabinet tasks: %w", err)
	}
	defer rows.Close()

	result := make([]statistics_service.CabinetTaskRow, 0, filter.Limit)
	for rows.Next() {
		var item statistics_service.CabinetTaskRow
		var startedAt, finishedAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.TransferID,
			&item.ItemTargetID,
			&item.TargetID,
			&item.CabinetID,
			&item.ItemPosition,
			&item.VendorCode,
			&item.GroupName,
			&item.State,
			&item.OutcomeClass,
			&item.OutcomeCode,
			&item.NMID,
			&item.PreparationStatus,
			&item.PublicationStatus,
			&item.MediaStatus,
			&item.OverallOutcome,
			&item.AttentionCode,
			&item.MediaActionState,
			&item.MediaOutcomeCode,
			&item.CreatedAt,
			&startedAt,
			&finishedAt,
		); err != nil {
			return statistics_service.CabinetTaskPage{},
				fmt.Errorf("scan statistics cabinet task: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.StartedAt = timePointer(startedAt)
		item.FinishedAt = timePointer(finishedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return statistics_service.CabinetTaskPage{},
			fmt.Errorf("iterate statistics cabinet tasks: %w", err)
	}
	return statistics_service.CabinetTaskPage{Tasks: result, Total: total}, nil
}
