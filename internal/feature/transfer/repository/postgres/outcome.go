package transfer_postgres_repository

import (
	"context"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

// Общие SQL-фрагменты агрегации результатов групп и расчёта мастер-исходов переноса.
const (
	// groupOutcomeCountsCTE вычисляет агрегированные количества исходов групп для заданного переноса ($1).
	groupOutcomeCountsCTE = `
		group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		)
	`

	// groupToTransferOutcomeExpression вычисляет результирующий master outcome переноса на основе group_counts.
	groupToTransferOutcomeExpression = `
		CASE
			WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0 THEN 'unresolved'
			WHEN group_counts.rejected = group_counts.total THEN 'rejected'
			WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
			ELSE 'succeeded'
		END
	`
)

// advanceTransferAfterPreparation продвигает состояние переноса после фиксации подготовки.
func advanceTransferAfterPreparation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	query := fmt.Sprintf(`
		WITH %s
		UPDATE wb.transfers AS transfer
		SET
			phase = CASE
				WHEN group_counts.terminal = group_counts.total THEN 'finished'
				WHEN transfer.phase = 'preparing' THEN 'awaiting_authorization'
				ELSE transfer.phase
			END,
			outcome = CASE
				WHEN group_counts.terminal <> group_counts.total THEN 'running'
				ELSE %s
			END,
			attention_code = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND (group_counts.unresolved > 0 OR group_counts.internal_error > 0)
					THEN 'preparation_requires_attention'
				ELSE NULL
			END,
			finished_at = CASE
				WHEN group_counts.terminal = group_counts.total THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
			revision = revision + 1,
			updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND NOT EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS group_target
				WHERE group_target.transfer_id = $1
					AND group_target.preparation_status NOT IN (
						'succeeded', 'rejected', 'unresolved'
					)
			);
	`, groupOutcomeCountsCTE, groupToTransferOutcomeExpression)
	if _, err := tx.Exec(ctx, query, transferID); err != nil {
		return fmt.Errorf("advance transfer after preparation: %w", err)
	}
	return nil
}

// finishPublicationTransfer завершает публикационный перенос при отсутствии running-групп.
func finishPublicationTransfer(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
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
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase IN ('publishing', 'reconciling', 'media')
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND NOT EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS running_group
				WHERE running_group.transfer_id = transfer.id
				  AND running_group.overall_outcome = 'running'
			);
	`, groupOutcomeCountsCTE, groupToTransferOutcomeExpression)
	result, err := tx.Exec(ctx, finish, transferID)
	if err != nil {
		return fmt.Errorf("finish publication transfer: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

// reduceTransferAfterPlanning пересчитывает и обновляет состояние переноса после создания плана с 0 действий.
func reduceTransferAfterPlanning(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	reduceTransfer := fmt.Sprintf(`
		WITH %s, item_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'terminal') AS terminal
			FROM wb.transfer_item_targets
			WHERE transfer_id = $1
		)
		UPDATE wb.transfers AS transfer
		SET phase = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND item_counts.terminal = item_counts.total THEN 'finished'
				ELSE transfer.phase
			END,
		    outcome = CASE
				WHEN group_counts.terminal <> group_counts.total
				  OR item_counts.terminal <> item_counts.total THEN 'running'
				ELSE %s
			END,
		    attention_code = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND (group_counts.unresolved > 0 OR group_counts.internal_error > 0)
					THEN 'terminal_item_requires_attention'
				ELSE NULL
			END,
		    finished_at = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND item_counts.terminal = item_counts.total THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts, item_counts
		WHERE transfer.id = $1
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count
			AND item_counts.total = transfer.item_targets_count;
	`, groupOutcomeCountsCTE, groupToTransferOutcomeExpression)
	result, err := tx.Exec(ctx, reduceTransfer, transferID)
	if err != nil {
		return fmt.Errorf("reduce transfer after zero-action plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}
