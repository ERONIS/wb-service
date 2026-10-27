package transfer_postgres_outcome

import (
	"context"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

// Общие SQL-фрагменты агрегации результатов групп и расчёта мастер-исходов переноса.
const (
	// ItemOutcomeCountColumns содержит общие счётчики результатов товаров для одной группы.
	ItemOutcomeCountColumns = `
		COUNT(*) AS total,
		COUNT(*) FILTER (WHERE item_target.state = 'terminal') AS terminal,
		COUNT(*) FILTER (WHERE item_target.outcome_class = 'rejected') AS rejected,
		COUNT(*) FILTER (WHERE item_target.outcome_class = 'unresolved') AS unresolved,
		COUNT(*) FILTER (WHERE item_target.outcome_class = 'internal_error') AS internal_error,
		COUNT(*) FILTER (
			WHERE item_target.outcome_class IN ('success', 'skipped')
		) AS accepted,
		COUNT(*) FILTER (
			WHERE item_target.outcome_class IN ('unresolved', 'internal_error')
			  AND item_target.attention_closed_at IS NULL
		) AS open_attention
	`

	// ItemToPublicationStatusExpression определяет статус публикации по результатам товаров.
	ItemToPublicationStatusExpression = `
		CASE
			WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
				THEN 'unresolved'
			WHEN item_counts.rejected > 0 THEN 'rejected'
			ELSE 'succeeded'
		END
	`

	// ItemToGroupOutcomeExpression определяет исход группы по результатам товаров.
	ItemToGroupOutcomeExpression = `
		CASE
			WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
				THEN 'unresolved'
			WHEN item_counts.rejected = item_counts.total THEN 'rejected'
			WHEN item_counts.rejected > 0 AND item_counts.accepted > 0 THEN 'partial'
			ELSE 'success'
		END
	`

	// GroupOutcomeCountsCTE вычисляет агрегированные количества исходов групп для заданного переноса ($1).
	GroupOutcomeCountsCTE = `
		group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error,
				COUNT(*) FILTER (
					WHERE overall_outcome IN ('unresolved', 'internal_error')
					  AND attention_code IS NOT NULL
				) AS open_attention
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		)
	`

	// GroupToTransferOutcomeExpression вычисляет общий исход переноса на основе счётчиков групп.
	GroupToTransferOutcomeExpression = `
		CASE
			WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0 THEN 'unresolved'
			WHEN group_counts.rejected = group_counts.total THEN 'rejected'
			WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
			ELSE 'succeeded'
		END
	`
)

// AdvanceTransferAfterPreparation продвигает состояние переноса после фиксации подготовки.
func AdvanceTransferAfterPreparation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) error {
	query := fmt.Sprintf(`
		WITH %s
		UPDATE wb.transfers AS transfer
		SET phase = 'finished',
		    outcome = %s,
		    attention_code = CASE
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'preparation_requires_attention'
				ELSE NULL
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND group_counts.terminal = group_counts.total
			AND NOT EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS group_target
				WHERE group_target.transfer_id = $1
					AND group_target.preparation_status NOT IN (
						'succeeded', 'rejected', 'unresolved'
					)
			);
	`, GroupOutcomeCountsCTE, GroupToTransferOutcomeExpression)
	if _, err := tx.Exec(ctx, query, transferID); err != nil {
		return fmt.Errorf("advance transfer after preparation: %w", err)
	}
	return nil
}

// FinishPublicationTransfer завершает публикационный перенос при отсутствии незавершённых групп.
func FinishPublicationTransfer(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) error {
	finish := fmt.Sprintf(`
		WITH %s
		UPDATE wb.transfers AS transfer
		SET phase = 'finished',
		    outcome = %s,
		    attention_code = CASE
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND NOT EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS running_group
				WHERE running_group.transfer_id = transfer.id
				  AND running_group.overall_outcome = 'running'
			);
	`, GroupOutcomeCountsCTE, GroupToTransferOutcomeExpression)
	result, err := tx.Exec(ctx, finish, transferID)
	if err != nil {
		return fmt.Errorf("finish publication transfer: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

// ReduceTransferAfterPlanning пересчитывает и обновляет состояние переноса после создания плана с 0 действий.
func ReduceTransferAfterPlanning(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) error {
	const itemCounts = `
		item_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'terminal') AS terminal
			FROM wb.transfer_item_targets
			WHERE transfer_id = $1
		)
	`
	counts := fmt.Sprintf(`
		WITH %s, %s
		SELECT
			EXISTS (
				SELECT 1
				FROM wb.transfers AS transfer
				WHERE transfer.id = $1
				  AND transfer.phase = 'active'
				  AND transfer.outcome = 'running'
				  AND transfer.group_targets_count = group_counts.total
				  AND transfer.item_targets_count = item_counts.total
			),
			group_counts.terminal = group_counts.total
				AND item_counts.terminal = item_counts.total
		FROM group_counts, item_counts;
	`, GroupOutcomeCountsCTE, itemCounts)
	var valid, complete bool
	if err := tx.QueryRow(ctx, counts, transferID).Scan(&valid, &complete); err != nil {
		return fmt.Errorf("check transfer after zero-action plan: %w", err)
	}
	if !valid {
		return transfer_preparation.ErrPreparationResultConflict
	}
	if !complete {
		return nil
	}

	finish := fmt.Sprintf(`
		WITH %s, %s
		UPDATE wb.transfers AS transfer
		SET phase = 'finished',
		    outcome = %s,
		    attention_code = CASE
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'terminal_item_requires_attention'
				ELSE NULL
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts, item_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND item_counts.total = transfer.item_targets_count
			AND group_counts.terminal = group_counts.total
			AND item_counts.terminal = item_counts.total;
	`, GroupOutcomeCountsCTE, itemCounts, GroupToTransferOutcomeExpression)
	result, err := tx.Exec(ctx, finish, transferID)
	if err != nil {
		return fmt.Errorf("finish transfer after zero-action plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}
