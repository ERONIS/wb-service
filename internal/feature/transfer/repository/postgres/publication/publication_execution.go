package transfer_postgres_publication

import (
	"context"
	"errors"
	"fmt"
	transfer_postgres_outcome "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/internal/outcome"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"

	"github.com/jackc/pgx/v5"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

var _ transfer_publication.PublicationExecutionResultRepository = (*PublicationRepository)(nil)

func (repository *PublicationRepository) EnsureActivePublication(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) error {
	if ctx == nil || tx == nil || transferID <= 0 {
		return errors.New("ensure active publication dependency is invalid")
	}
	const query = `
		SELECT id
		FROM wb.transfers
		WHERE id = $1
			AND phase = 'active'
			AND outcome = 'running'
		FOR UPDATE;
	`
	var activeTransferID transfer_model.TransferID
	err := tx.QueryRow(ctx, query, transferID).Scan(&activeTransferID)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_preparation.ErrPreparationResultConflict
	}
	if err != nil {
		return fmt.Errorf("ensure active transfer publication: %w", err)
	}
	return nil
}

func (repository *PublicationRepository) ApplyPublicationActionResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationActionResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("apply publication action result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}

	if err := finishPublicationActionItems(ctx, tx, command); err != nil {
		return err
	}
	if err := skipPublicationActionMedia(ctx, tx, command); err != nil {
		return err
	}
	affectedGroupSet := make(map[int64]struct{}, len(command.Items)+len(command.SkipMediaForGroups))
	for _, item := range command.Items {
		affectedGroupSet[item.GroupTargetID] = struct{}{}
	}
	for _, groupTargetID := range command.SkipMediaForGroups {
		affectedGroupSet[groupTargetID] = struct{}{}
	}
	affectedGroups := make([]int64, 0, len(affectedGroupSet))
	for groupTargetID := range affectedGroupSet {
		affectedGroups = append(affectedGroups, groupTargetID)
	}
	if err := finishPublicationActionGroups(
		ctx,
		tx,
		command.TransferID,
		affectedGroups,
	); err != nil {
		return err
	}

	return advanceOrFinishPublicationTransfer(
		ctx,
		tx,
		command.TransferID,
	)
}

func finishPublicationActionItems(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationActionResultCommand,
) error {
	groupTargetIDs := make([]int64, len(command.Items))
	itemTargetIDs := make([]int64, len(command.Items))
	outcomeClasses := make([]string, len(command.Items))
	outcomeCodes := make([]string, len(command.Items))
	nmIDs := make([]int64, len(command.Items))
	for index, item := range command.Items {
		groupTargetIDs[index] = item.GroupTargetID
		itemTargetIDs[index] = item.TransferItemTargetID
		outcomeClasses[index] = string(item.OutcomeClass)
		outcomeCodes[index] = item.OutcomeCode
		nmIDs[index] = item.NMID
	}
	const finish = `
		WITH terminal_item AS (
			SELECT *
			FROM UNNEST(
				$3::bigint[], $4::bigint[], $5::text[], $6::text[], $7::bigint[]
			) AS source(
				group_target_id, item_target_id, outcome_class, outcome_code, nm_id
			)
		)
		UPDATE wb.transfer_item_targets AS item_target
		SET state = 'terminal',
		    outcome_class = terminal_item.outcome_class,
		    outcome_code = terminal_item.outcome_code,
		    nm_id = NULLIF(terminal_item.nm_id, 0),
		    finished_at = CURRENT_TIMESTAMP,
		    revision = item_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM terminal_item
		WHERE item_target.transfer_id = $1
			AND item_target.source_action_id = $2
			AND item_target.group_target_id = terminal_item.group_target_id
			AND item_target.id = terminal_item.item_target_id
			AND item_target.state = 'running'
			AND EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS group_target
				JOIN wb.publication_actions AS product_action
				  ON product_action.transfer_id = group_target.transfer_id
				 AND product_action.plan_id = group_target.publication_plan_id
				JOIN wb.publication_action_members AS product_member
				  ON product_member.transfer_id = product_action.transfer_id
				 AND product_member.action_id = product_action.id
				WHERE group_target.transfer_id = item_target.transfer_id
				  AND group_target.id = item_target.group_target_id
				  AND product_action.id = $2
				  AND product_action.kind IN ('create_group', 'add_to_group')
				  AND product_action.state = 'terminal'
				  AND product_action.outcome_class IS NOT NULL
				  AND product_member.group_target_id = item_target.group_target_id
				  AND product_member.transfer_item_target_id = item_target.id
				  AND product_member.outcome_class IS NOT NULL
			)
			AND EXISTS (
				SELECT 1
				FROM wb.transfers AS active_transfer
				WHERE active_transfer.id = item_target.transfer_id
				  AND active_transfer.phase = 'active'
				  AND active_transfer.outcome = 'running'
			);
	`
	result, err := tx.Exec(
		ctx,
		finish,
		command.TransferID,
		command.ActionID,
		groupTargetIDs,
		itemTargetIDs,
		outcomeClasses,
		outcomeCodes,
		nmIDs,
	)
	if err != nil {
		return fmt.Errorf("finish publication item projections: %w", err)
	}
	if result.RowsAffected() != int64(len(command.Items)) {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

func skipPublicationActionMedia(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationActionResultCommand,
) error {
	if len(command.SkipMediaForGroups) == 0 {
		return nil
	}
	const skip = `
		UPDATE wb.transfer_group_targets AS group_target
		SET media_status = 'skipped',
		    revision = group_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE group_target.transfer_id = $1
			AND group_target.id = ANY($2::bigint[])
			AND group_target.media_status IN ('not_started', 'running', 'skipped')
			AND EXISTS (
				SELECT 1
				FROM wb.publication_actions AS product_action
				JOIN wb.publication_action_members AS product_member
				  ON product_member.transfer_id = product_action.transfer_id
				 AND product_member.action_id = product_action.id
				WHERE product_action.transfer_id = group_target.transfer_id
				  AND product_action.plan_id = group_target.publication_plan_id
				  AND product_action.id = $3
				  AND product_action.kind IN ('create_group', 'add_to_group')
				  AND product_action.state = 'terminal'
				  AND product_action.outcome_class IS NOT NULL
				  AND product_member.group_target_id = group_target.id
				  AND product_member.outcome_class IS NOT NULL
			)
			AND EXISTS (
				SELECT 1
				FROM wb.transfers AS active_transfer
				WHERE active_transfer.id = group_target.transfer_id
				  AND active_transfer.phase = 'active'
				  AND active_transfer.outcome = 'running'
			);
	`
	result, err := tx.Exec(ctx, skip, command.TransferID, command.SkipMediaForGroups, command.ActionID)
	if err != nil {
		return fmt.Errorf("skip publication media projections: %w", err)
	}
	if result.RowsAffected() != int64(len(command.SkipMediaForGroups)) {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

func finishPublicationActionGroups(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	groupTargetIDs []int64,
) error {
	finish := fmt.Sprintf(`
		WITH affected_group AS (
			SELECT UNNEST($2::bigint[]) AS group_target_id
		), item_counts AS (
			SELECT
				item_target.group_target_id,
				%s
			FROM wb.transfer_item_targets AS item_target
			JOIN affected_group
			  ON affected_group.group_target_id = item_target.group_target_id
			WHERE item_target.transfer_id = $1
			GROUP BY item_target.group_target_id
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET publication_status = %s,
		    media_status = CASE
				WHEN group_target.media_status = 'not_started' THEN 'running'
				ELSE group_target.media_status
			END,
		    overall_outcome = CASE
				WHEN group_target.media_status = 'unresolved' THEN 'unresolved'
				WHEN group_target.media_status = 'rejected' THEN 'partial'
				WHEN group_target.media_status IN ('not_started', 'running') THEN 'running'
				ELSE %s
			END,
		    attention_code = CASE
				WHEN group_target.media_status = 'unresolved'
					THEN 'media_requires_attention'
				WHEN group_target.media_status IN ('not_started', 'running') THEN NULL
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    finished_at = CASE
				WHEN group_target.media_status IN ('not_started', 'running') THEN NULL
				ELSE CURRENT_TIMESTAMP
			END,
		    revision = group_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM item_counts
		WHERE group_target.transfer_id = $1
			AND group_target.id = item_counts.group_target_id
			AND group_target.overall_outcome = 'running'
			AND item_counts.total > 0
			AND item_counts.terminal = item_counts.total
			AND EXISTS (
				SELECT 1
				FROM wb.transfers AS active_transfer
				WHERE active_transfer.id = group_target.transfer_id
				  AND active_transfer.phase = 'active'
				  AND active_transfer.outcome = 'running'
			);
	`, transfer_postgres_outcome.ItemOutcomeCountColumns, transfer_postgres_outcome.ItemToPublicationStatusExpression, transfer_postgres_outcome.ItemToGroupOutcomeExpression)
	result, err := tx.Exec(ctx, finish, transferID, groupTargetIDs)
	if err != nil {
		return fmt.Errorf("finish publication group projections: %w", err)
	}
	if result.RowsAffected() != int64(len(groupTargetIDs)) {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

func (repository *PublicationRepository) CorrectPublicationItemResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.CorrectPublicationItemResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("correct publication item result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := transfer_postgres_workflow.LockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Phase != transfer_model.PhaseFinished ||
		transfer.Outcome == transfer_model.OutcomeRunning ||
		transfer.Outcome == transfer_model.OutcomeFailed {
		return transfer_preparation.ErrPreparationResultConflict
	}

	var nmID any
	if command.NMID > 0 {
		nmID = command.NMID
	}
	const correctItem = `
		UPDATE wb.transfer_item_targets
		SET outcome_class = $9,
		    outcome_code = $10,
		    nm_id = $11,
		    attention_closed_at = CASE
		        WHEN $12 THEN CURRENT_TIMESTAMP
		        ELSE NULL
		    END,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND group_target_id = $2
			AND id = $3
			AND source_action_id = $4
			AND revision = $5
			AND state = 'terminal'
			AND outcome_class = $6
			AND outcome_code = $7
			AND COALESCE(nm_id, 0) = $8
			AND (attention_closed_at IS NOT NULL) = $13;
	`
	result, err := tx.Exec(
		ctx,
		correctItem,
		command.TransferID,
		command.GroupTargetID,
		command.TransferItemTargetID,
		command.ActionID,
		command.ExpectedItemRevision,
		command.ExpectedOutcomeClass,
		command.ExpectedOutcomeCode,
		command.ExpectedNMID,
		command.OutcomeClass,
		command.OutcomeCode,
		nmID,
		command.CloseAttentionNoRetry,
		command.ExpectedAttentionClosed,
	)
	if err != nil {
		return fmt.Errorf("correct publication item projection: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}

	reduceGroup := fmt.Sprintf(`
		WITH item_counts AS (
			SELECT
				%s
			FROM wb.transfer_item_targets AS item_target
			WHERE item_target.transfer_id = $1 AND item_target.group_target_id = $2
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET publication_status = %s,
		    overall_outcome = CASE
				WHEN group_target.media_status = 'unresolved' THEN 'unresolved'
				WHEN group_target.media_status = 'rejected' THEN 'partial'
				ELSE %s
			END,
		    attention_code = CASE
				WHEN item_counts.open_attention > 0
					THEN 'publication_requires_attention'
				WHEN group_target.media_status = 'unresolved'
					THEN COALESCE(group_target.attention_code, 'publication_requires_attention')
				ELSE NULL
			END,
		    revision = group_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM item_counts
		WHERE group_target.transfer_id = $1
			AND group_target.id = $2
			AND group_target.overall_outcome <> 'running'
			AND item_counts.total > 0
			AND item_counts.terminal = item_counts.total;
	`, transfer_postgres_outcome.ItemOutcomeCountColumns, transfer_postgres_outcome.ItemToPublicationStatusExpression, transfer_postgres_outcome.ItemToGroupOutcomeExpression)
	result, err = tx.Exec(ctx, reduceGroup, command.TransferID, command.GroupTargetID)
	if err != nil {
		return fmt.Errorf("reduce corrected publication group: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}

	reduceTransfer := fmt.Sprintf(`
		WITH %s
		UPDATE wb.transfers AS transfer
		SET outcome = %s,
		    attention_code = CASE
				WHEN group_counts.open_attention > 0
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'finished'
			AND transfer.outcome IN ('succeeded', 'partial', 'rejected', 'unresolved')
			AND group_counts.total = transfer.group_targets_count
			AND group_counts.terminal = group_counts.total;
	`, transfer_postgres_outcome.GroupOutcomeCountsCTE, transfer_postgres_outcome.GroupToTransferOutcomeExpression)
	result, err = tx.Exec(ctx, reduceTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("reduce corrected transfer result: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}

func (repository *PublicationRepository) ApplyPublicationMediaResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_publication.ApplyPublicationMediaResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("apply publication media result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}

	mediaStatus := "unresolved"
	switch command.OutcomeClass {
	case transfer_model.ResultSuccess:
		mediaStatus = "succeeded"
	case transfer_model.ResultSkipped:
		mediaStatus = "skipped"
	case transfer_model.ResultRejected:
		mediaStatus = "rejected"
	case transfer_model.ResultUnresolved, transfer_model.ResultInternalError:
		mediaStatus = "unresolved"
	default:
		return transfer_preparation.ErrPreparationResultConflict
	}
	finishGroup := fmt.Sprintf(`
		WITH item_counts AS (
			SELECT
				%s
			FROM wb.transfer_item_targets AS item_target
			WHERE item_target.transfer_id = $1 AND item_target.group_target_id = $2
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET media_status = $3,
		    overall_outcome = CASE
				WHEN $3 = 'unresolved' THEN 'unresolved'
				WHEN $3 = 'rejected' THEN 'partial'
				ELSE %s
			END,
		    attention_code = CASE
				WHEN $3 = 'unresolved' THEN $4
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = group_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM item_counts
		WHERE group_target.transfer_id = $1
			AND group_target.id = $2
			AND group_target.publication_plan_id = $5
			AND group_target.media_status = 'running'
			AND group_target.overall_outcome = 'running'
			AND group_target.publication_status IN (
				'succeeded', 'rejected', 'unresolved', 'skipped'
			)
			AND item_counts.total > 0
			AND item_counts.terminal = item_counts.total
			AND EXISTS (
				SELECT 1
				FROM wb.transfers AS active_transfer
				WHERE active_transfer.id = group_target.transfer_id
				  AND active_transfer.outcome = 'running'
			)
			AND EXISTS (
				SELECT 1
				FROM wb.publication_actions AS media_action
				JOIN wb.publication_action_members AS media_member
				  ON media_member.transfer_id = media_action.transfer_id
				 AND media_member.action_id = media_action.id
				WHERE media_action.transfer_id = group_target.transfer_id
				  AND media_action.plan_id = group_target.publication_plan_id
				  AND media_action.id = $6
				  AND media_action.kind = 'upload_media'
				  AND media_action.state = 'terminal'
				  AND media_action.outcome_class IS NOT NULL
				  AND media_member.group_target_id = group_target.id
				  AND media_member.outcome_class IS NOT NULL
			)
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_actions AS other_media_action
				JOIN wb.publication_action_members AS other_media_member
				  ON other_media_member.transfer_id = other_media_action.transfer_id
				 AND other_media_member.action_id = other_media_action.id
				WHERE other_media_action.transfer_id = group_target.transfer_id
				  AND other_media_action.plan_id = group_target.publication_plan_id
				  AND other_media_action.kind = 'upload_media'
				  AND other_media_member.group_target_id = group_target.id
				  AND other_media_action.state NOT IN ('terminal', 'superseded')
			);
	`, transfer_postgres_outcome.ItemOutcomeCountColumns, transfer_postgres_outcome.ItemToGroupOutcomeExpression)
	result, err := tx.Exec(
		ctx,
		finishGroup,
		command.TransferID,
		command.GroupTargetID,
		mediaStatus,
		command.OutcomeCode,
		command.PlanID,
		command.ActionID,
	)
	if err != nil {
		return fmt.Errorf("finish publication media group projection: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_preparation.ErrPreparationResultConflict
	}

	return advanceOrFinishPublicationTransfer(
		ctx,
		tx,
		command.TransferID,
	)
}

func advanceOrFinishPublicationTransfer(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
) error {
	transfer, err := transfer_postgres_workflow.LockTransferByID(ctx, tx, transferID)
	if err != nil {
		return err
	}
	if transfer.Outcome != transfer_model.OutcomeRunning ||
		transfer.Phase != transfer_model.PhaseActive {
		return transfer_preparation.ErrPreparationResultConflict
	}

	const hasRunningGroups = `
		SELECT EXISTS (
			SELECT 1
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1 AND overall_outcome = 'running'
		);
	`
	var running bool
	if err := tx.QueryRow(ctx, hasRunningGroups, transferID).Scan(&running); err != nil {
		return fmt.Errorf("check running publication groups: %w", err)
	}
	if running {
		return nil
	}
	return transfer_postgres_outcome.FinishPublicationTransfer(ctx, tx, transferID)
}

func (repository *PublicationRepository) ResetPublicationPlanning(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	planID int64,
) error {
	if ctx == nil || tx == nil || transferID <= 0 || planID <= 0 {
		return errors.New("reset publication planning dependency is invalid")
	}
	transfer, err := transfer_postgres_workflow.LockTransferByID(ctx, tx, transferID)
	if err != nil {
		return err
	}
	if !transfer.Phase.AllowsGroupWork() ||
		transfer.Outcome != transfer_model.OutcomeRunning {
		return transfer_preparation.ErrPreparationResultConflict
	}
	const resetItems = `
		UPDATE wb.transfer_item_targets AS item_target
		SET state = 'running',
		    outcome_class = NULL,
		    outcome_code = NULL,
		    nm_id = NULL,
		    source_action_id = NULL,
		    attention_closed_at = NULL,
		    finished_at = NULL,
		    revision = item_target.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM wb.transfer_group_targets AS group_target
		WHERE item_target.transfer_id = $1
			AND group_target.transfer_id = item_target.transfer_id
			AND group_target.id = item_target.group_target_id
			AND group_target.publication_plan_id = $2
			AND group_target.preparation_status = 'succeeded';
	`
	if _, err := tx.Exec(ctx, resetItems, transferID, planID); err != nil {
		return fmt.Errorf("reset publication item projections: %w", err)
	}
	const resetGroups = `
		UPDATE wb.transfer_group_targets
		SET publication_status = 'not_started',
		    media_status = 'not_started',
		    overall_outcome = 'running',
		    attention_code = NULL,
		    publication_plan_id = NULL,
		    finished_at = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND publication_plan_id = $2
			AND preparation_status = 'succeeded';
	`
	result, err := tx.Exec(ctx, resetGroups, transferID, planID)
	if err != nil {
		return fmt.Errorf("reset publication group projections: %w", err)
	}
	if result.RowsAffected() == 0 {
		return transfer_preparation.ErrPreparationResultConflict
	}
	return nil
}
