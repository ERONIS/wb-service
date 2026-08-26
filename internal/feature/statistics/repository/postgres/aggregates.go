package statistics_postgres_repository

import (
	"context"
	"fmt"
	"time"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
)

func (repository *Repository) AggregateTransfers(
	ctx context.Context,
	filter statistics_service.AggregateFilter,
) (statistics_service.TransferTotals, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	builder := transferAggregateWhere(filter)
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE transfer.phase = 'finished'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'succeeded'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'partial'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'rejected'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'unresolved'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'failed'),
			COUNT(*) FILTER (WHERE transfer.outcome = 'cancelled'),
			COUNT(*) FILTER (WHERE transfer.attention_code IS NOT NULL),
			COALESCE(AVG(EXTRACT(EPOCH FROM (transfer.finished_at - transfer.started_at)))
				FILTER (WHERE transfer.finished_at IS NOT NULL), 0)::double precision
		FROM wb.statistics_transfer_facts AS transfer` + builder.clause()
	var totals statistics_service.TransferTotals
	var seconds float64
	err := repository.pool.QueryRow(ctx, query, builder.arguments...).Scan(
		&totals.Total, &totals.Terminal, &totals.Succeeded, &totals.Partial,
		&totals.Rejected, &totals.Unresolved, &totals.Failed, &totals.Cancelled,
		&totals.RequiresAttention, &seconds,
	)
	if err != nil {
		return statistics_service.TransferTotals{}, fmt.Errorf("aggregate statistics transfers: %w", err)
	}
	totals.AverageDuration = durationFromSeconds(seconds)
	return totals, nil
}

func transferAggregateWhere(filter statistics_service.AggregateFilter) whereBuilder {
	var builder whereBuilder
	if filter.TransferID > 0 {
		builder.add("transfer.transfer_id = $%d", filter.TransferID)
	}
	if filter.CabinetID != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.transfer_targets AS target
			WHERE target.transfer_id = transfer.transfer_id
			  AND target.cabinet_id = $%d
		)`, filter.CabinetID)
	}
	if filter.ActionKind != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_action_facts AS action_filter
			WHERE action_filter.transfer_id = transfer.transfer_id
			  AND action_filter.kind = $%d
		)`, filter.ActionKind)
	}
	if filter.Phase != "" {
		builder.add("transfer.phase = $%d", filter.Phase)
	}
	if filter.OutcomeClass != "" {
		outcome := filter.OutcomeClass
		switch outcome {
		case "success":
			outcome = "succeeded"
		case "internal_error":
			outcome = "failed"
		}
		builder.add("transfer.outcome = $%d", outcome)
	}
	if filter.OutcomeCode != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_item_facts AS item_filter
			WHERE item_filter.transfer_id = transfer.transfer_id
			  AND item_filter.outcome_code = $%[1]d
			UNION ALL
			SELECT 1 FROM wb.statistics_action_facts AS action_filter
			WHERE action_filter.transfer_id = transfer.transfer_id
			  AND action_filter.outcome_code = $%[1]d
		)`, filter.OutcomeCode)
	}
	if filter.DeliveryState != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_attempt_facts AS attempt_filter
			WHERE attempt_filter.transfer_id = transfer.transfer_id
			  AND attempt_filter.delivery_state = $%d
		)`, filter.DeliveryState)
	}
	if filter.ResponseDisposition != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_attempt_facts AS attempt_filter
			WHERE attempt_filter.transfer_id = transfer.transfer_id
			  AND attempt_filter.response_disposition = $%d
		)`, filter.ResponseDisposition)
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

func (repository *Repository) AggregateItems(
	ctx context.Context,
	filter statistics_service.AggregateFilter,
) (statistics_service.ItemTotals, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	builder := itemAggregateWhere(filter)
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE item.state = 'terminal'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'success'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'skipped'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'rejected'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'partial'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'unresolved'),
			COUNT(*) FILTER (WHERE item.outcome_class = 'internal_error'),
			COUNT(*) FILTER (
				WHERE item.outcome_class IN ('unresolved', 'internal_error')
				  AND item.attention_closed_at IS NULL
			),
			COALESCE(AVG(EXTRACT(EPOCH FROM (item.finished_at - item.started_at)))
				FILTER (WHERE item.finished_at IS NOT NULL), 0)::double precision
		FROM wb.statistics_item_facts AS item` + builder.clause()
	var totals statistics_service.ItemTotals
	var seconds float64
	err := repository.pool.QueryRow(ctx, query, builder.arguments...).Scan(
		&totals.Total, &totals.Terminal, &totals.Success, &totals.Skipped,
		&totals.Rejected, &totals.Partial, &totals.Unresolved,
		&totals.InternalError, &totals.RequiresAttention, &seconds,
	)
	if err != nil {
		return statistics_service.ItemTotals{}, fmt.Errorf("aggregate statistics items: %w", err)
	}
	totals.AverageDuration = durationFromSeconds(seconds)
	return totals, nil
}

func itemAggregateWhere(filter statistics_service.AggregateFilter) whereBuilder {
	var builder whereBuilder
	if filter.TransferID > 0 {
		builder.add("item.transfer_id = $%d", filter.TransferID)
	}
	if filter.CabinetID != "" {
		builder.add("item.cabinet_id = $%d", filter.CabinetID)
	}
	if filter.ActionKind != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_action_facts AS action_filter
			WHERE action_filter.transfer_id = item.transfer_id
			  AND action_filter.action_id = item.source_action_id
			  AND action_filter.kind = $%d
		)`, filter.ActionKind)
	}
	if filter.Phase != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_transfer_facts AS transfer_filter
			WHERE transfer_filter.transfer_id = item.transfer_id
			  AND transfer_filter.phase = $%d
		)`, filter.Phase)
	}
	if filter.OutcomeClass != "" {
		builder.add("item.outcome_class = $%d", filter.OutcomeClass)
	}
	if filter.OutcomeCode != "" {
		builder.add("item.outcome_code = $%d", filter.OutcomeCode)
	}
	if filter.DeliveryState != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_attempt_facts AS attempt_filter
			WHERE attempt_filter.transfer_id = item.transfer_id
			  AND attempt_filter.action_id = item.source_action_id
			  AND attempt_filter.delivery_state = $%d
		)`, filter.DeliveryState)
	}
	if filter.ResponseDisposition != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_attempt_facts AS attempt_filter
			WHERE attempt_filter.transfer_id = item.transfer_id
			  AND attempt_filter.action_id = item.source_action_id
			  AND attempt_filter.response_disposition = $%d
		)`, filter.ResponseDisposition)
	}
	if filter.RequiresAttention != nil {
		expression := `COALESCE((item.outcome_class IN ('unresolved', 'internal_error')
			AND item.attention_closed_at IS NULL), false)`
		if *filter.RequiresAttention {
			builder.addRaw(expression)
		} else {
			builder.addRaw("NOT " + expression)
		}
	}
	if filter.CreatedFrom != nil {
		builder.add("item.created_at >= $%d", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		builder.add("item.created_at < $%d", *filter.CreatedTo)
	}
	return builder
}

func (repository *Repository) AggregateActions(
	ctx context.Context,
	filter statistics_service.AggregateFilter,
) (statistics_service.ActionTotals, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	builder := actionAggregateWhere(filter)
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE action.state IN ('terminal', 'superseded')),
			COUNT(*) FILTER (WHERE action.outcome_class = 'success'),
			COUNT(*) FILTER (WHERE action.outcome_class = 'skipped'),
			COUNT(*) FILTER (WHERE action.outcome_class = 'rejected'),
			COUNT(*) FILTER (WHERE action.outcome_class = 'partial'),
			COUNT(*) FILTER (WHERE action.outcome_class = 'unresolved'),
			COUNT(*) FILTER (WHERE action.outcome_class = 'internal_error'),
			COUNT(attempt.attempt_id),
			COUNT(attempt.attempt_id) FILTER (
				WHERE attempt.delivery_state IN ('response_received', 'unknown_delivery')
			),
			COUNT(attempt.attempt_id) FILTER (
				WHERE attempt.delivery_state = 'unknown_delivery'
			),
			COALESCE(AVG(EXTRACT(EPOCH FROM (action.finished_at - action.started_at)))
				FILTER (WHERE action.finished_at IS NOT NULL AND action.started_at IS NOT NULL), 0)::double precision
		FROM wb.statistics_action_facts AS action
		LEFT JOIN wb.statistics_attempt_facts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.action_id` + builder.clause()
	var totals statistics_service.ActionTotals
	var seconds float64
	err := repository.pool.QueryRow(ctx, query, builder.arguments...).Scan(
		&totals.Total, &totals.Terminal, &totals.Success, &totals.Skipped,
		&totals.Rejected, &totals.Partial, &totals.Unresolved,
		&totals.InternalError, &totals.Attempts, &totals.RealWBRoundTrips,
		&totals.UnknownDelivery, &seconds,
	)
	if err != nil {
		return statistics_service.ActionTotals{}, fmt.Errorf("aggregate statistics actions: %w", err)
	}
	totals.AverageDuration = durationFromSeconds(seconds)
	return totals, nil
}

func actionAggregateWhere(filter statistics_service.AggregateFilter) whereBuilder {
	var builder whereBuilder
	if filter.TransferID > 0 {
		builder.add("action.transfer_id = $%d", filter.TransferID)
	}
	if filter.CabinetID != "" {
		builder.add("action.cabinet_id = $%d", filter.CabinetID)
	}
	if filter.ActionKind != "" {
		builder.add("action.kind = $%d", filter.ActionKind)
	}
	if filter.Phase != "" {
		builder.add(`EXISTS (
			SELECT 1 FROM wb.statistics_transfer_facts AS transfer_filter
			WHERE transfer_filter.transfer_id = action.transfer_id
			  AND transfer_filter.phase = $%d
		)`, filter.Phase)
	}
	if filter.OutcomeClass != "" {
		builder.add("action.outcome_class = $%d", filter.OutcomeClass)
	}
	if filter.OutcomeCode != "" {
		builder.add("action.outcome_code = $%d", filter.OutcomeCode)
	}
	if filter.DeliveryState != "" {
		builder.add("attempt.delivery_state = $%d", filter.DeliveryState)
	}
	if filter.ResponseDisposition != "" {
		builder.add("attempt.response_disposition = $%d", filter.ResponseDisposition)
	}
	if filter.RequiresAttention != nil {
		expression := "COALESCE(action.outcome_class IN ('unresolved', 'internal_error'), false)"
		if *filter.RequiresAttention {
			builder.addRaw(expression)
		} else {
			builder.addRaw("NOT (" + expression + ")")
		}
	}
	if filter.CreatedFrom != nil {
		builder.add("action.created_at >= $%d", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		builder.add("action.created_at < $%d", *filter.CreatedTo)
	}
	return builder
}

func (repository *Repository) AggregateErrors(
	ctx context.Context,
	filter statistics_service.AggregateFilter,
) ([]statistics_service.ErrorGroup, error) {
	ctx, cancel := repository.queryContext(ctx)
	defer cancel()
	var builder whereBuilder
	if filter.TransferID > 0 {
		builder.add("fact.transfer_id = $%d", filter.TransferID)
	}
	if filter.CabinetID != "" {
		builder.add("fact.cabinet_id = $%d", filter.CabinetID)
	}
	if filter.ActionKind != "" {
		builder.add("fact.action_kind = $%d", filter.ActionKind)
	}
	if filter.Phase != "" {
		builder.add("fact.phase = $%d", filter.Phase)
	}
	if filter.OutcomeClass != "" {
		builder.add("fact.outcome_class = $%d", filter.OutcomeClass)
	}
	if filter.OutcomeCode != "" {
		builder.add("fact.outcome_code = $%d", filter.OutcomeCode)
	}
	if filter.DeliveryState != "" {
		builder.add("fact.delivery_state = $%d", filter.DeliveryState)
	}
	if filter.ResponseDisposition != "" {
		builder.add("fact.response_disposition = $%d", filter.ResponseDisposition)
	}
	if filter.RequiresAttention != nil {
		builder.add("fact.requires_attention = $%d", *filter.RequiresAttention)
	}
	if filter.CreatedFrom != nil {
		builder.add("fact.created_at >= $%d", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		builder.add("fact.created_at < $%d", *filter.CreatedTo)
	}
	query := `
		WITH facts AS (
			SELECT
				'item'::text AS source,
				item.transfer_id, item.cabinet_id, action.kind AS action_kind,
				transfer.phase, item.outcome_class, item.outcome_code,
				attempt.delivery_state, attempt.response_disposition,
				(item.outcome_class IN ('unresolved', 'internal_error')
				 AND item.attention_closed_at IS NULL) AS requires_attention,
				item.created_at
			FROM wb.statistics_item_facts AS item
			JOIN wb.statistics_transfer_facts AS transfer
			  ON transfer.transfer_id = item.transfer_id
			LEFT JOIN wb.statistics_action_facts AS action
			  ON action.transfer_id = item.transfer_id
			 AND action.action_id = item.source_action_id
			LEFT JOIN wb.statistics_attempt_facts AS attempt
			  ON attempt.transfer_id = item.transfer_id
			 AND attempt.action_id = item.source_action_id
			WHERE item.outcome_class IN ('rejected', 'partial', 'unresolved', 'internal_error')
			UNION ALL
			SELECT
				'action'::text AS source,
				action.transfer_id, action.cabinet_id, action.kind,
				transfer.phase, action.outcome_class, action.outcome_code,
				attempt.delivery_state, attempt.response_disposition,
				(action.outcome_class IN ('unresolved', 'internal_error')) AS requires_attention,
				action.created_at
			FROM wb.statistics_action_facts AS action
			JOIN wb.statistics_transfer_facts AS transfer
			  ON transfer.transfer_id = action.transfer_id
			LEFT JOIN wb.statistics_attempt_facts AS attempt
			  ON attempt.transfer_id = action.transfer_id
			 AND attempt.action_id = action.action_id
			WHERE action.outcome_class IN ('rejected', 'partial', 'unresolved', 'internal_error')
			UNION ALL
			SELECT
				'attempt'::text AS source,
				attempt.transfer_id, attempt.cabinet_id, attempt.action_kind,
				transfer.phase,
				CASE
					WHEN attempt.response_disposition = 'rejected_proven' THEN 'rejected'
					WHEN attempt.delivery_state = 'unknown_delivery'
					  OR attempt.response_disposition = 'uncertain' THEN 'unresolved'
					ELSE 'internal_error'
				END AS outcome_class,
				attempt.safe_error_code AS outcome_code,
				attempt.delivery_state, attempt.response_disposition,
				(attempt.delivery_state = 'unknown_delivery'
				 OR attempt.response_disposition = 'uncertain') AS requires_attention,
				attempt.started_at AS created_at
			FROM wb.statistics_attempt_facts AS attempt
			JOIN wb.statistics_transfer_facts AS transfer
			  ON transfer.transfer_id = attempt.transfer_id
			WHERE attempt.safe_error_code IS NOT NULL
			UNION ALL
			SELECT
				'error_batch'::text AS source,
				correlation.transfer_id, batch.cabinet_id, action.kind,
				transfer.phase, 'rejected'::text AS outcome_class,
				error_code.value AS outcome_code,
				attempt.delivery_state, attempt.response_disposition,
				COALESCE(action.outcome_class IN ('unresolved', 'internal_error'), false)
					AS requires_attention,
				batch.created_at
			FROM wb.statistics_error_batch_facts AS batch
			CROSS JOIN LATERAL unnest(batch.error_codes) AS error_code(value)
			LEFT JOIN wb.publication_error_correlations AS correlation
			  ON correlation.error_batch_id = batch.error_batch_id
			LEFT JOIN wb.statistics_action_facts AS action
			  ON action.transfer_id = correlation.transfer_id
			 AND action.action_id = correlation.action_id
			LEFT JOIN wb.statistics_attempt_facts AS attempt
			  ON attempt.transfer_id = correlation.transfer_id
			 AND attempt.attempt_id = correlation.attempt_id
			LEFT JOIN wb.statistics_transfer_facts AS transfer
			  ON transfer.transfer_id = correlation.transfer_id
		)
		SELECT fact.source, fact.outcome_class, fact.outcome_code, COUNT(*)
		FROM facts AS fact` + builder.clause() + `
		GROUP BY fact.source, fact.outcome_class, fact.outcome_code
		ORDER BY COUNT(*) DESC, fact.source, fact.outcome_class, fact.outcome_code`
	rows, err := repository.pool.Query(ctx, query, builder.arguments...)
	if err != nil {
		return nil, fmt.Errorf("aggregate statistics errors: %w", err)
	}
	defer rows.Close()
	result := make([]statistics_service.ErrorGroup, 0)
	for rows.Next() {
		var item statistics_service.ErrorGroup
		if err := rows.Scan(&item.Source, &item.OutcomeClass, &item.OutcomeCode, &item.Count); err != nil {
			return nil, fmt.Errorf("scan statistics error aggregate: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statistics error aggregates: %w", err)
	}
	return result, nil
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
