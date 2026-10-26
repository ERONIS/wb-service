package transfer_postgres_publication

import (
	"context"
	"errors"
	"fmt"
	transfer_postgres_outcome "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/internal/outcome"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

var _ transfer_publication.PublicationPlanResultRepository = (*PublicationRepository)(nil)

func (repository *PublicationRepository) ApplyPublicationPlanResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationPlanResultCommand,
) error {
	if ctx == nil {
		return errors.New("apply publication plan result dependency is nil")
	}
	if tx == nil {
		return errors.New("apply publication plan result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := transfer_postgres_workflow.LockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Outcome != transfer_model.OutcomeRunning ||
		!transfer.Phase.AllowsGroupWork() {
		return transfer_preparation.ErrPreparationResultConflict
	}

	if err := updatePublicationPlanGroups(ctx, tx, command); err != nil {
		return err
	}
	if err := updatePublicationPlanItems(ctx, tx, command); err != nil {
		return err
	}

	if command.ActionCount > 0 {
		return nil
	}

	return transfer_postgres_outcome.ReduceTransferAfterPlanning(ctx, tx, command.TransferID)
}

func updatePublicationPlanGroups(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationPlanResultCommand,
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
		finished[index] = group.Status != transfer_publication.PublicationProjectionRunning && !group.HasMediaAction
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
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

func updatePublicationPlanItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationPlanResultCommand,
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
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}
