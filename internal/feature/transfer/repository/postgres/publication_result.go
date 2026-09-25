package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) ApplyPublicationPlanResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPublicationPlanResultCommand,
) error {
	if tx == nil {
		return errors.New("apply publication plan result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := lockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Outcome != transfer_service.OutcomeRunning ||
		!publicationPlanningPhase(transfer.Phase) {
		return transfer_service.ErrPreparationResultConflict
	}

	if err := updatePublicationPlanGroups(ctx, tx, command); err != nil {
		return err
	}
	if err := updatePublicationPlanItems(ctx, tx, command); err != nil {
		return err
	}

	if command.ActionCount > 0 {
		const touchTransfer = `
			UPDATE wb.transfers
			SET phase = CASE
					WHEN phase = 'preparing' THEN 'awaiting_authorization'
					ELSE phase
				END,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
				AND phase IN (
					'preparing', 'awaiting_authorization', 'publishing',
					'reconciling', 'media'
				)
				AND outcome = 'running';
		`
		result, err := tx.Exec(ctx, touchTransfer, command.TransferID)
		if err != nil {
			return fmt.Errorf("touch transfer publication plan: %w", err)
		}
		if result.RowsAffected() != 1 {
			return transfer_service.ErrPreparationResultConflict
		}
		return nil
	}

	const reduceTransfer = `
		WITH group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		), item_counts AS (
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
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'unresolved'
				WHEN group_counts.rejected = group_counts.total THEN 'rejected'
				WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
				ELSE 'succeeded'
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
	`
	result, err := tx.Exec(ctx, reduceTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("reduce transfer after zero-action plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func updatePublicationPlanGroups(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPublicationPlanResultCommand,
) error {
	if len(command.Groups) == 0 {
		return nil
	}
	groupTargetIDs := make([]int64, len(command.Groups))
	publicationStatuses := make([]string, len(command.Groups))
	mediaStatuses := make([]string, len(command.Groups))
	overallOutcomes := make([]string, len(command.Groups))
	finished := make([]bool, len(command.Groups))
	for index, group := range command.Groups {
		groupTargetIDs[index] = group.GroupTargetID
		publicationStatuses[index] = string(group.Status)
		mediaStatuses[index] = "skipped"
		overallOutcomes[index] = string(group.OutcomeClass)
		finished[index] = group.Status != transfer_service.PublicationProjectionRunning && !group.HasMediaAction
		if !finished[index] {
			overallOutcomes[index] = "running"
			if group.HasMediaAction {
				mediaStatuses[index] = "running"
			}
		}
	}
	const update = `
		WITH planned_group AS (
			SELECT *
			FROM UNNEST(
				$2::bigint[], $3::text[], $4::text[], $5::text[], $6::boolean[]
			) AS source(
				group_target_id, publication_status, media_status,
				overall_outcome, finished
			)
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET publication_status = planned_group.publication_status,
		    media_status = planned_group.media_status,
		    overall_outcome = planned_group.overall_outcome,
		    attention_code = NULL,
		    publication_plan_id = $7,
		    finished_at = CASE
				WHEN planned_group.finished THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
		    revision = group_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM planned_group
		WHERE group_target.transfer_id = $1
			AND group_target.id = planned_group.group_target_id
			AND group_target.preparation_status = 'succeeded'
			AND group_target.publication_status = 'not_started'
			AND group_target.overall_outcome = 'running';
	`
	result, err := tx.Exec(
		ctx,
		update,
		command.TransferID,
		groupTargetIDs,
		publicationStatuses,
		mediaStatuses,
		overallOutcomes,
		finished,
		command.PlanID,
	)
	if err != nil {
		return fmt.Errorf("update publication group projections: %w", err)
	}
	if result.RowsAffected() != int64(len(command.Groups)) {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func updatePublicationPlanItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPublicationPlanResultCommand,
) error {
	if len(command.Items) == 0 {
		return nil
	}
	groupTargetIDs := make([]int64, len(command.Items))
	itemTargetIDs := make([]int64, len(command.Items))
	actionIDs := make([]int64, len(command.Items))
	outcomeClasses := make([]string, len(command.Items))
	outcomeCodes := make([]string, len(command.Items))
	nmIDs := make([]int64, len(command.Items))
	for index, item := range command.Items {
		groupTargetIDs[index] = item.GroupTargetID
		itemTargetIDs[index] = item.TransferItemTargetID
		actionIDs[index] = item.SourceActionID
		outcomeClasses[index] = string(item.OutcomeClass)
		outcomeCodes[index] = item.OutcomeCode
		nmIDs[index] = item.NMID
	}
	const update = `
		WITH planned_item AS (
			SELECT *
			FROM UNNEST(
				$2::bigint[], $3::bigint[], $4::bigint[],
				$5::text[], $6::text[], $7::bigint[]
			) AS source(
				group_target_id, item_target_id, action_id,
				outcome_class, outcome_code, nm_id
			)
		)
		UPDATE wb.transfer_item_targets AS item_target
		SET state = CASE
				WHEN planned_item.action_id > 0 THEN 'running'
				ELSE 'terminal'
			END,
		    source_action_id = NULLIF(planned_item.action_id, 0),
		    outcome_class = NULLIF(planned_item.outcome_class, ''),
		    outcome_code = NULLIF(planned_item.outcome_code, ''),
		    nm_id = NULLIF(planned_item.nm_id, 0),
		    finished_at = CASE
				WHEN planned_item.action_id > 0 THEN NULL
				ELSE CURRENT_TIMESTAMP
			END,
		    revision = item_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM planned_item
		WHERE item_target.transfer_id = $1
			AND item_target.group_target_id = planned_item.group_target_id
			AND item_target.id = planned_item.item_target_id
			AND item_target.state = 'running'
			AND item_target.source_action_id IS NULL;
	`
	result, err := tx.Exec(
		ctx,
		update,
		command.TransferID,
		groupTargetIDs,
		itemTargetIDs,
		actionIDs,
		outcomeClasses,
		outcomeCodes,
		nmIDs,
	)
	if err != nil {
		return fmt.Errorf("update publication item projections: %w", err)
	}
	if result.RowsAffected() != int64(len(command.Items)) {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func publicationPlanningPhase(phase transfer_service.Phase) bool {
	switch phase {
	case transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia:
		return true
	default:
		return false
	}
}

var _ transfer_service.PublicationPlanResultRepository = (*Repository)(nil)
