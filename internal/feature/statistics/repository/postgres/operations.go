package statistics_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const operationColumns = `
	transfer.transfer_id,
	transfer.batch_id,
	COALESCE(batch.source_session_id, batch.source_reference_id),
	transfer.phase,
	transfer.outcome,
	COALESCE(transfer.attention_code, ''),
	transfer.cohort_name,
	transfer.items_count,
	transfer.groups_count,
	transfer.targets_count,
	transfer.item_targets_count,
	transfer.group_targets_count,
	transfer.created_at,
	transfer.started_at,
	transfer.finished_at`

func (repository *Repository) ListOperations(
	ctx context.Context,
	filter statistics_service.OperationFilter,
) ([]statistics_service.OperationRow, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	builder := operationWhere(filter)
	builder.arguments = append(builder.arguments, filter.Limit, filter.Offset)
	query := `SELECT ` + operationColumns + `
		FROM wb.statistics_transfer_facts AS transfer
		JOIN wb.card_batches AS batch ON batch.id = transfer.batch_id` + builder.clause() + `
		ORDER BY transfer.transfer_id DESC
		LIMIT $` + fmt.Sprint(len(builder.arguments)-1) + `
		OFFSET $` + fmt.Sprint(len(builder.arguments))
	rows, err := repository.pool.Query(ctx, query, builder.arguments...)
	if err != nil {
		return nil, fmt.Errorf("list statistics operations: %w", err)
	}
	defer rows.Close()

	result := make([]statistics_service.OperationRow, 0, filter.Limit)
	for rows.Next() {
		row, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan statistics operation: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statistics operations: %w", err)
	}
	return result, nil
}

func operationWhere(filter statistics_service.OperationFilter) whereBuilder {
	var builder whereBuilder
	if filter.TransferID > 0 {
		builder.add("transfer.transfer_id = $%d", filter.TransferID)
	}
	if filter.BatchID > 0 {
		builder.add("transfer.batch_id = $%d", filter.BatchID)
	}
	if filter.CabinetID != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.transfer_targets AS target
			WHERE target.transfer_id = transfer.transfer_id
			  AND target.cabinet_id = $%d
		)`, filter.CabinetID)
	}
	if filter.Phase != "" {
		builder.add("transfer.phase = $%d", filter.Phase)
	}
	if filter.Outcome != "" {
		builder.add("transfer.outcome = $%d", filter.Outcome)
	}
	if filter.RequiresAttention != nil {
		if *filter.RequiresAttention {
			builder.addRaw("transfer.attention_code IS NOT NULL")
		} else {
			builder.addRaw("transfer.attention_code IS NULL")
		}
	}
	if filter.CreatedFrom != nil {
		builder.add("transfer.created_at >= $%d", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		builder.add("transfer.created_at < $%d", *filter.CreatedTo)
	}
	return builder
}

func (repository *Repository) GetOperation(
	ctx context.Context,
	transferID int64,
) (statistics_service.OperationDetails, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()

	operation, err := repository.getOperationRow(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	groups, groupsTruncated, err := repository.listGroups(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	items, itemsTruncated, err := repository.listItems(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	actions, actionsTruncated, err := repository.listActions(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	attempts, attemptsTruncated, err := repository.listAttempts(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	authorizations, authorizationsTruncated, err := repository.listAuthorizations(ctx, transferID)
	if err != nil {
		return statistics_service.OperationDetails{}, err
	}
	return statistics_service.OperationDetails{
		Operation:               operation,
		Groups:                  groups,
		Items:                   items,
		Actions:                 actions,
		Attempts:                attempts,
		Authorizations:          authorizations,
		GroupsTruncated:         groupsTruncated,
		ItemsTruncated:          itemsTruncated,
		ActionsTruncated:        actionsTruncated,
		AttemptsTruncated:       attemptsTruncated,
		AuthorizationsTruncated: authorizationsTruncated,
	}, nil
}

func (repository *Repository) getOperationRow(
	ctx context.Context,
	transferID int64,
) (statistics_service.OperationRow, error) {
	query := `SELECT ` + operationColumns + `
		FROM wb.statistics_transfer_facts AS transfer
		JOIN wb.card_batches AS batch ON batch.id = transfer.batch_id
		WHERE transfer.transfer_id = $1`
	row, err := scanOperation(repository.pool.QueryRow(ctx, query, transferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return statistics_service.OperationRow{}, statistics_service.ErrOperationNotFound
	}
	if err != nil {
		return statistics_service.OperationRow{}, fmt.Errorf("get statistics operation: %w", err)
	}
	return row, nil
}

func scanOperation(row rowScanner) (statistics_service.OperationRow, error) {
	var result statistics_service.OperationRow
	var finishedAt pgtype.Timestamptz
	err := row.Scan(
		&result.TransferID,
		&result.BatchID,
		&result.SessionID,
		&result.Phase,
		&result.Outcome,
		&result.AttentionCode,
		&result.CohortName,
		&result.ItemsCount,
		&result.GroupsCount,
		&result.TargetsCount,
		&result.ItemTargetsCount,
		&result.GroupTargetsCount,
		&result.CreatedAt,
		&result.StartedAt,
		&finishedAt,
	)
	if finishedAt.Valid {
		value := finishedAt.Time.UTC()
		result.FinishedAt = &value
	}
	result.CreatedAt = result.CreatedAt.UTC()
	result.StartedAt = result.StartedAt.UTC()
	return result, err
}

func (repository *Repository) listGroups(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.GroupTargetRow, bool, error) {
	const query = `
		SELECT
			group_target.id,
			group_target.source_group_id,
			source_group.source_group_name,
			target.id,
			target.position,
			target.cabinet_id,
			group_target.preparation_status,
			group_target.publication_status,
			group_target.media_status,
			group_target.overall_outcome,
			COALESCE(group_target.attention_code, ''),
			COUNT(item_target.id),
			COUNT(item_target.id) FILTER (WHERE item_target.state = 'terminal'),
			COUNT(item_target.id) FILTER (
				WHERE item_target.state = 'terminal'
				  AND item_target.outcome_class IN ('unresolved', 'internal_error')
				  AND item_target.attention_closed_at IS NULL
			),
			group_target.started_at,
			group_target.finished_at
		FROM wb.transfer_group_targets AS group_target
		JOIN wb.transfer_groups AS source_group
		  ON source_group.transfer_id = group_target.transfer_id
		 AND source_group.id = group_target.source_group_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = group_target.transfer_id
		 AND target.id = group_target.target_id
		LEFT JOIN wb.transfer_item_targets AS item_target
		  ON item_target.transfer_id = group_target.transfer_id
		 AND item_target.group_target_id = group_target.id
		WHERE group_target.transfer_id = $1
		GROUP BY group_target.id, source_group.source_group_name, target.id
		ORDER BY target.position, source_group.source_group_name, group_target.id
		LIMIT $2`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		statistics_service.DetailsPageSize+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list statistics group targets: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.GroupTargetRow, 0)
	for rows.Next() {
		var item statistics_service.GroupTargetRow
		var startedAt, finishedAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.GroupTargetID, &item.SourceGroupID, &item.GroupName,
			&item.TargetID, &item.TargetPosition, &item.CabinetID,
			&item.PreparationStatus, &item.PublicationStatus, &item.MediaStatus,
			&item.OverallOutcome, &item.AttentionCode, &item.ItemsTotal,
			&item.ItemsTerminal, &item.ItemsAttention, &startedAt, &finishedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan statistics group target: %w", err)
		}
		item.StartedAt = timePointer(startedAt)
		item.FinishedAt = timePointer(finishedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate statistics group targets: %w", err)
	}
	return boundedGroups(result)
}

func (repository *Repository) listItems(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.ItemRow, bool, error) {
	const query = `
		SELECT
			item.transfer_id, item.item_target_id, item.group_target_id,
			item.transfer_item_id, item.target_id, item.target_position,
			item.cabinet_id, item.item_position, item.vendor_code, item.state,
			COALESCE(item.outcome_class, ''), COALESCE(item.outcome_code, ''),
			COALESCE(item.nm_id, 0), COALESCE(item.source_action_id, 0),
			item.attention_closed_at, item.created_at, item.started_at, item.finished_at
		FROM wb.statistics_item_facts AS item
		WHERE item.transfer_id = $1
		ORDER BY item.target_position, item.item_position, item.item_target_id
		LIMIT $2`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		statistics_service.DetailsPageSize+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list statistics items: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.ItemRow, 0)
	for rows.Next() {
		var item statistics_service.ItemRow
		var attentionAt, startedAt, finishedAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.TransferID, &item.ItemTargetID, &item.GroupTargetID,
			&item.TransferItemID, &item.TargetID, &item.TargetPosition,
			&item.CabinetID, &item.ItemPosition, &item.VendorCode, &item.State,
			&item.OutcomeClass, &item.OutcomeCode, &item.NMID, &item.SourceActionID,
			&attentionAt, &item.CreatedAt, &startedAt, &finishedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan statistics item: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.AttentionClosedAt = timePointer(attentionAt)
		item.StartedAt = timePointer(startedAt)
		item.FinishedAt = timePointer(finishedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate statistics items: %w", err)
	}
	return boundedItems(result)
}

func (repository *Repository) listActions(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.ActionRow, bool, error) {
	const query = `
		SELECT
			fact.transfer_id, fact.action_id, fact.plan_id, fact.target_id,
			fact.target_position, fact.cabinet_id, fact.kind, fact.state,
			action.revision, COALESCE(fact.outcome_class, ''),
			COALESCE(fact.outcome_code, ''), COALESCE(fact.authorization_id, 0),
			COUNT(member.id), fact.created_at, fact.started_at, fact.finished_at
		FROM wb.statistics_action_facts AS fact
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = fact.transfer_id AND action.id = fact.action_id
		LEFT JOIN wb.publication_action_members AS member
		  ON member.transfer_id = fact.transfer_id AND member.action_id = fact.action_id
		WHERE fact.transfer_id = $1
		GROUP BY fact.transfer_id, fact.action_id, fact.plan_id, fact.target_id,
			fact.target_position, fact.cabinet_id, fact.kind, fact.state,
			action.revision, fact.outcome_class, fact.outcome_code,
			fact.authorization_id, fact.created_at, fact.started_at, fact.finished_at
		ORDER BY fact.target_position, fact.action_id
		LIMIT $2`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		statistics_service.DetailsPageSize+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list statistics actions: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.ActionRow, 0)
	for rows.Next() {
		item, err := scanAction(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan statistics action: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate statistics actions: %w", err)
	}
	return boundedActions(result)
}

func (repository *Repository) listAttempts(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.AttemptRow, bool, error) {
	const query = `
		SELECT
			attempt.transfer_id, attempt.attempt_id, attempt.action_id,
			attempt.authorization_id, attempt.recheck_observation_id,
			COALESCE(attempt.attribution_id, 0), attempt.plan_id, attempt.target_id,
			attempt.target_position, attempt.cabinet_id, attempt.action_kind,
			attempt.delivery_state, COALESCE(attempt.http_status, 0),
			COALESCE(attempt.response_disposition, ''),
			COALESCE(attempt.classifier_version, 0),
			COALESCE(attempt.safe_error_code, ''), attempt.unmatched_count,
			attempt.error_baseline_cabinet_id, attempt.error_cursor_revision,
			attempt.error_cursor_updated_at, attempt.error_cursor_batch_uuid,
			attempt.error_baseline_captured_at, attempt.started_at, attempt.finished_at
		FROM wb.statistics_attempt_facts AS attempt
		WHERE attempt.transfer_id = $1
		ORDER BY attempt.started_at, attempt.attempt_id
		LIMIT $2`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		statistics_service.DetailsPageSize+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list statistics attempts: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.AttemptRow, 0)
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan statistics attempt: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate statistics attempts: %w", err)
	}
	return boundedAttempts(result)
}

func (repository *Repository) listAuthorizations(
	ctx context.Context,
	transferID int64,
) ([]statistics_service.AuthorizationRow, bool, error) {
	const query = `
		SELECT
			auth.transfer_id, auth.authorization_id,
			auth.plan_id, auth.revision, auth.state,
			auth.requested_at, auth.approved_at,
			auth.expires_at, auth.revoked_at,
			auth.closed_at, COALESCE(auth.safe_reason_code, ''),
			auth.created_at
		FROM wb.statistics_authorization_facts AS auth
		WHERE auth.transfer_id = $1
		ORDER BY auth.authorization_id
		LIMIT $2`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		statistics_service.DetailsPageSize+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list statistics authorizations: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.AuthorizationRow, 0)
	for rows.Next() {
		var item statistics_service.AuthorizationRow
		var approvedAt, revokedAt, closedAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.TransferID, &item.AuthorizationID, &item.PlanID,
			&item.Revision, &item.State, &item.RequestedAt, &approvedAt,
			&item.ExpiresAt, &revokedAt, &closedAt, &item.SafeReasonCode,
			&item.CreatedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan statistics authorization: %w", err)
		}
		item.RequestedAt = item.RequestedAt.UTC()
		item.ExpiresAt = item.ExpiresAt.UTC()
		item.CreatedAt = item.CreatedAt.UTC()
		item.ApprovedAt = timePointer(approvedAt)
		item.RevokedAt = timePointer(revokedAt)
		item.ClosedAt = timePointer(closedAt)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate statistics authorizations: %w", err)
	}
	if len(result) <= statistics_service.DetailsPageSize {
		return result, false, nil
	}
	return result[:statistics_service.DetailsPageSize], true, nil
}

func (repository *Repository) GetAction(
	ctx context.Context,
	actionID int64,
) (statistics_service.ActionDetails, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	const actionQuery = `
		SELECT
			fact.transfer_id, fact.action_id, fact.plan_id, fact.target_id,
			fact.target_position, fact.cabinet_id, fact.kind, fact.state,
			action.revision, COALESCE(fact.outcome_class, ''),
			COALESCE(fact.outcome_code, ''), COALESCE(fact.authorization_id, 0),
			COUNT(member.id), fact.created_at, fact.started_at, fact.finished_at
		FROM wb.statistics_action_facts AS fact
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = fact.transfer_id AND action.id = fact.action_id
		LEFT JOIN wb.publication_action_members AS member
		  ON member.transfer_id = fact.transfer_id AND member.action_id = fact.action_id
		WHERE fact.action_id = $1
		GROUP BY fact.transfer_id, fact.action_id, fact.plan_id, fact.target_id,
			fact.target_position, fact.cabinet_id, fact.kind, fact.state,
			action.revision, fact.outcome_class, fact.outcome_code,
			fact.authorization_id, fact.created_at, fact.started_at, fact.finished_at`
	action, err := scanAction(repository.pool.QueryRow(ctx, actionQuery, actionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return statistics_service.ActionDetails{}, statistics_service.ErrActionNotFound
	}
	if err != nil {
		return statistics_service.ActionDetails{}, fmt.Errorf("get statistics action: %w", err)
	}
	const attemptQuery = `
		SELECT
			attempt.transfer_id, attempt.attempt_id, attempt.action_id,
			attempt.authorization_id, attempt.recheck_observation_id,
			COALESCE(attempt.attribution_id, 0), attempt.plan_id, attempt.target_id,
			attempt.target_position, attempt.cabinet_id, attempt.action_kind,
			attempt.delivery_state, COALESCE(attempt.http_status, 0),
			COALESCE(attempt.response_disposition, ''),
			COALESCE(attempt.classifier_version, 0),
			COALESCE(attempt.safe_error_code, ''), attempt.unmatched_count,
			attempt.error_baseline_cabinet_id, attempt.error_cursor_revision,
			attempt.error_cursor_updated_at, attempt.error_cursor_batch_uuid,
			attempt.error_baseline_captured_at, attempt.started_at, attempt.finished_at
		FROM wb.statistics_attempt_facts AS attempt
		WHERE attempt.action_id = $1`
	attempt, err := scanAttempt(repository.pool.QueryRow(ctx, attemptQuery, actionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return statistics_service.ActionDetails{Action: action}, nil
	}
	if err != nil {
		return statistics_service.ActionDetails{}, fmt.Errorf("get statistics attempt: %w", err)
	}
	return statistics_service.ActionDetails{Action: action, Attempt: &attempt}, nil
}

func scanAction(row rowScanner) (statistics_service.ActionRow, error) {
	var item statistics_service.ActionRow
	var startedAt, finishedAt pgtype.Timestamptz
	err := row.Scan(
		&item.TransferID, &item.ActionID, &item.PlanID, &item.TargetID,
		&item.TargetPosition, &item.CabinetID, &item.Kind, &item.State,
		&item.Revision, &item.OutcomeClass, &item.OutcomeCode,
		&item.AuthorizationID, &item.MembersCount, &item.CreatedAt,
		&startedAt, &finishedAt,
	)
	item.CreatedAt = item.CreatedAt.UTC()
	item.StartedAt = timePointer(startedAt)
	item.FinishedAt = timePointer(finishedAt)
	return item, err
}

func scanAttempt(row rowScanner) (statistics_service.AttemptRow, error) {
	var item statistics_service.AttemptRow
	var cursorAt, finishedAt pgtype.Timestamptz
	err := row.Scan(
		&item.TransferID, &item.AttemptID, &item.ActionID,
		&item.AuthorizationID, &item.RecheckObservationID, &item.AttributionID,
		&item.PlanID, &item.TargetID, &item.TargetPosition, &item.CabinetID,
		&item.ActionKind, &item.DeliveryState, &item.HTTPStatus,
		&item.ResponseDisposition, &item.ClassifierVersion,
		&item.SafeErrorCode, &item.UnmatchedCount,
		&item.ErrorBaselineCabinetID, &item.ErrorCursorRevision, &cursorAt,
		&item.ErrorCursorBatchUUID, &item.ErrorBaselineCapturedAt,
		&item.StartedAt, &finishedAt,
	)
	item.ErrorBaselineCapturedAt = item.ErrorBaselineCapturedAt.UTC()
	item.StartedAt = item.StartedAt.UTC()
	item.ErrorCursorUpdatedAt = timePointer(cursorAt)
	item.FinishedAt = timePointer(finishedAt)
	return item, err
}

func boundedGroups(rows []statistics_service.GroupTargetRow) ([]statistics_service.GroupTargetRow, bool, error) {
	if len(rows) <= statistics_service.DetailsPageSize {
		return rows, false, nil
	}
	return rows[:statistics_service.DetailsPageSize], true, nil
}

func boundedItems(rows []statistics_service.ItemRow) ([]statistics_service.ItemRow, bool, error) {
	if len(rows) <= statistics_service.DetailsPageSize {
		return rows, false, nil
	}
	return rows[:statistics_service.DetailsPageSize], true, nil
}

func boundedActions(rows []statistics_service.ActionRow) ([]statistics_service.ActionRow, bool, error) {
	if len(rows) <= statistics_service.DetailsPageSize {
		return rows, false, nil
	}
	return rows[:statistics_service.DetailsPageSize], true, nil
}

func boundedAttempts(rows []statistics_service.AttemptRow) ([]statistics_service.AttemptRow, bool, error) {
	if len(rows) <= statistics_service.DetailsPageSize {
		return rows, false, nil
	}
	return rows[:statistics_service.DetailsPageSize], true, nil
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

var _ statistics_service.Reader = (*Repository)(nil)
