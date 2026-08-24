package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) BeginPublicationAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	if tx == nil {
		return errors.New("begin publication action: DBTX is nil")
	}
	const update = `
		UPDATE wb.transfers
		SET phase = 'publishing',
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND phase IN ('awaiting_authorization', 'publishing', 'reconciling')
			AND outcome = 'running';
	`
	result, err := tx.Exec(ctx, update, transferID)
	if err != nil {
		return fmt.Errorf("begin transfer publication action: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) MarkPublicationReconciling(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	if tx == nil {
		return errors.New("mark publication reconciling: DBTX is nil")
	}
	const update = `
		UPDATE wb.transfers
		SET phase = 'reconciling',
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND phase IN ('publishing', 'reconciling')
			AND outcome = 'running';
	`
	result, err := tx.Exec(ctx, update, transferID)
	if err != nil {
		return fmt.Errorf("mark transfer publication reconciling: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) BeginPublicationMedia(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	if tx == nil {
		return errors.New("begin publication media: DBTX is nil")
	}
	const update = `
		UPDATE wb.transfers
		SET phase = 'media',
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND phase IN (
				'awaiting_authorization', 'publishing', 'reconciling', 'media'
			)
			AND outcome = 'running';
	`
	result, err := tx.Exec(ctx, update, transferID)
	if err != nil {
		return fmt.Errorf("begin transfer publication media: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) ApplyPublicationActionResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPublicationActionResultCommand,
) error {
	if tx == nil {
		return errors.New("apply publication action result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := lockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Outcome != transfer_service.OutcomeRunning ||
		(transfer.Phase != transfer_service.PhasePublishing &&
			transfer.Phase != transfer_service.PhaseReconciling) {
		return transfer_service.ErrPreparationResultConflict
	}

	affectedGroups := make(map[int64]struct{})
	for _, item := range command.Items {
		var nmID any
		if item.NMID > 0 {
			nmID = item.NMID
		}
		const finishItem = `
			UPDATE wb.transfer_item_targets
			SET state = 'terminal',
			    outcome_class = $5,
			    outcome_code = $6,
			    nm_id = $7,
			    finished_at = CURRENT_TIMESTAMP,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND group_target_id = $2
				AND id = $3
				AND source_action_id = $4
				AND state = 'running';
		`
		result, err := tx.Exec(
			ctx,
			finishItem,
			command.TransferID,
			item.GroupTargetID,
			item.TransferItemTargetID,
			command.ActionID,
			item.OutcomeClass,
			item.OutcomeCode,
			nmID,
		)
		if err != nil {
			return fmt.Errorf("finish publication item projection: %w", err)
		}
		if result.RowsAffected() != 1 {
			return transfer_service.ErrPreparationResultConflict
		}
		affectedGroups[item.GroupTargetID] = struct{}{}
	}

	for _, groupTargetID := range command.SkipMediaForGroups {
		const skipMedia = `
			UPDATE wb.transfer_group_targets
			SET media_status = 'skipped',
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND id = $2
				AND media_status IN ('not_started', 'running', 'skipped');
		`
		result, err := tx.Exec(ctx, skipMedia, command.TransferID, groupTargetID)
		if err != nil {
			return fmt.Errorf("skip publication media projection: %w", err)
		}
		if result.RowsAffected() != 1 {
			return transfer_service.ErrPreparationResultConflict
		}
		affectedGroups[groupTargetID] = struct{}{}
	}

	for groupTargetID := range affectedGroups {
		const finishGroup = `
			WITH item_counts AS (
				SELECT
					COUNT(*) AS total,
					COUNT(*) FILTER (WHERE state = 'terminal') AS terminal,
					COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
					COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
					COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
					COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
				FROM wb.transfer_item_targets
				WHERE transfer_id = $1 AND group_target_id = $2
			)
			UPDATE wb.transfer_group_targets AS group_target
			SET publication_status = CASE
					WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
						THEN 'unresolved'
					WHEN item_counts.rejected = item_counts.total THEN 'rejected'
					WHEN item_counts.rejected > 0 THEN 'rejected'
					ELSE 'succeeded'
				END,
				    media_status = CASE
						WHEN group_target.media_status = 'not_started' THEN 'running'
						ELSE group_target.media_status
					END,
				    overall_outcome = CASE
						WHEN group_target.media_status = 'unresolved'
							THEN 'unresolved'
						WHEN group_target.media_status = 'rejected'
							THEN 'partial'
						WHEN group_target.media_status IN ('not_started', 'running')
							THEN 'running'
						WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
						THEN 'unresolved'
					WHEN item_counts.rejected = item_counts.total THEN 'rejected'
					WHEN item_counts.rejected > 0 AND item_counts.accepted > 0 THEN 'partial'
					ELSE 'success'
				END,
				    attention_code = CASE
						WHEN group_target.media_status = 'unresolved'
							THEN 'media_requires_attention'
						WHEN group_target.media_status IN ('not_started', 'running')
							THEN NULL
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
				AND group_target.id = $2
					AND group_target.overall_outcome = 'running'
				AND item_counts.total > 0
				AND item_counts.terminal = item_counts.total;
		`
		result, err := tx.Exec(ctx, finishGroup, command.TransferID, groupTargetID)
		if err != nil {
			return fmt.Errorf("finish publication group projection: %w", err)
		}
		if result.RowsAffected() != 1 {
			return transfer_service.ErrPreparationResultConflict
		}
	}

	const reduceTransfer = `
		WITH group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE overall_outcome IN ('success', 'skipped')) AS accepted
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
					WHEN group_counts.terminal = group_counts.total THEN 'finished'
					WHEN item_counts.terminal = item_counts.total THEN 'media'
					ELSE 'reconciling'
			END,
		    outcome = CASE
				WHEN group_counts.terminal <> group_counts.total THEN 'running'
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'unresolved'
				WHEN group_counts.rejected = group_counts.total THEN 'rejected'
				WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
				ELSE 'succeeded'
			END,
		    attention_code = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND (group_counts.unresolved > 0 OR group_counts.internal_error > 0)
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    finished_at = CASE
				WHEN group_counts.terminal = group_counts.total THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
			FROM group_counts, item_counts
		WHERE transfer.id = $1
			AND transfer.outcome = 'running'
				AND group_counts.total = transfer.group_targets_count
				AND item_counts.total = transfer.item_targets_count;
	`
	result, err := tx.Exec(ctx, reduceTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("reduce transfer publication result: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) CorrectPublicationItemResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.CorrectPublicationItemResultCommand,
) error {
	if tx == nil {
		return errors.New("correct publication item result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := lockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Phase != transfer_service.PhaseFinished ||
		transfer.Outcome == transfer_service.OutcomeRunning ||
		transfer.Outcome == transfer_service.OutcomeFailed ||
		transfer.Outcome == transfer_service.OutcomeCancelled {
		return transfer_service.ErrPreparationResultConflict
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
		return transfer_service.ErrPreparationResultConflict
	}

	const reduceGroup = `
		WITH item_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'terminal') AS terminal,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted,
				COUNT(*) FILTER (
					WHERE outcome_class IN ('unresolved', 'internal_error')
					  AND attention_closed_at IS NULL
				) AS open_attention
			FROM wb.transfer_item_targets
			WHERE transfer_id = $1 AND group_target_id = $2
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET publication_status = CASE
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'unresolved'
				WHEN item_counts.rejected > 0 THEN 'rejected'
				ELSE 'succeeded'
			END,
		    overall_outcome = CASE
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'unresolved'
				WHEN item_counts.rejected = item_counts.total THEN 'rejected'
				WHEN item_counts.rejected > 0 AND item_counts.accepted > 0 THEN 'partial'
				ELSE 'success'
			END,
		    attention_code = CASE
				WHEN item_counts.open_attention > 0
					THEN 'publication_requires_attention'
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
	`
	result, err = tx.Exec(ctx, reduceGroup, command.TransferID, command.GroupTargetID)
	if err != nil {
		return fmt.Errorf("reduce corrected publication group: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}

	const reduceTransfer = `
		WITH group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE overall_outcome IN ('success', 'skipped')) AS accepted,
				COUNT(*) FILTER (
					WHERE overall_outcome IN ('unresolved', 'internal_error')
					  AND attention_code IS NOT NULL
				) AS open_attention
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		)
		UPDATE wb.transfers AS transfer
		SET outcome = CASE
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'unresolved'
				WHEN group_counts.rejected = group_counts.total THEN 'rejected'
				WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
				ELSE 'succeeded'
			END,
		    attention_code = CASE
				WHEN group_counts.open_attention > 0
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'finished'
			AND transfer.outcome IN ('succeeded', 'partial', 'rejected', 'unresolved')
			AND group_counts.total = transfer.group_targets_count
			AND group_counts.terminal = group_counts.total;
	`
	result, err = tx.Exec(ctx, reduceTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("reduce corrected transfer result: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) ApplyPublicationMediaResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPublicationMediaResultCommand,
) error {
	if tx == nil {
		return errors.New("apply publication media result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transfer, err := lockTransferByID(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	if transfer.Outcome != transfer_service.OutcomeRunning ||
		transfer.Phase != transfer_service.PhaseMedia {
		return transfer_service.ErrPreparationResultConflict
	}

	mediaStatus := "unresolved"
	switch command.OutcomeClass {
	case transfer_service.ResultSuccess:
		mediaStatus = "succeeded"
	case transfer_service.ResultSkipped:
		mediaStatus = "skipped"
	case transfer_service.ResultRejected:
		mediaStatus = "rejected"
	case transfer_service.ResultUnresolved, transfer_service.ResultInternalError:
		mediaStatus = "unresolved"
	default:
		return transfer_service.ErrPreparationResultConflict
	}
	const finishGroup = `
		WITH item_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'terminal') AS terminal,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
			FROM wb.transfer_item_targets
			WHERE transfer_id = $1 AND group_target_id = $2
		)
		UPDATE wb.transfer_group_targets AS group_target
		SET media_status = $3,
		    overall_outcome = CASE
				WHEN $3 = 'unresolved' THEN 'unresolved'
				WHEN $3 = 'rejected' THEN 'partial'
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'unresolved'
				WHEN item_counts.rejected = item_counts.total THEN 'rejected'
				WHEN item_counts.rejected > 0 AND item_counts.accepted > 0 THEN 'partial'
				ELSE 'success'
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
			AND group_target.media_status = 'running'
			AND group_target.overall_outcome = 'running'
			AND group_target.publication_status IN (
				'succeeded', 'rejected', 'unresolved', 'skipped'
			)
			AND item_counts.total > 0
			AND item_counts.terminal = item_counts.total;
	`
	result, err := tx.Exec(
		ctx,
		finishGroup,
		command.TransferID,
		command.GroupTargetID,
		mediaStatus,
		command.OutcomeCode,
	)
	if err != nil {
		return fmt.Errorf("finish publication media group projection: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}

	const reduceTransfer = `
		WITH group_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE overall_outcome <> 'running') AS terminal,
				COUNT(*) FILTER (WHERE overall_outcome = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE overall_outcome = 'partial') AS partial,
				COUNT(*) FILTER (WHERE overall_outcome = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE overall_outcome = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE overall_outcome IN ('success', 'skipped')) AS accepted
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		)
		UPDATE wb.transfers AS transfer
		SET phase = CASE
				WHEN group_counts.terminal = group_counts.total THEN 'finished'
				ELSE 'media'
			END,
		    outcome = CASE
				WHEN group_counts.terminal <> group_counts.total THEN 'running'
				WHEN group_counts.unresolved > 0 OR group_counts.internal_error > 0
					THEN 'unresolved'
				WHEN group_counts.rejected = group_counts.total THEN 'rejected'
				WHEN group_counts.rejected > 0 OR group_counts.partial > 0 THEN 'partial'
				ELSE 'succeeded'
			END,
		    attention_code = CASE
				WHEN group_counts.terminal = group_counts.total
				 AND (group_counts.unresolved > 0 OR group_counts.internal_error > 0)
					THEN 'publication_requires_attention'
				ELSE NULL
			END,
		    finished_at = CASE
				WHEN group_counts.terminal = group_counts.total THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM group_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'media'
			AND transfer.outcome = 'running'
			AND group_counts.total = transfer.group_targets_count;
	`
	result, err = tx.Exec(ctx, reduceTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("reduce transfer publication media result: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) ResetPublicationPlanning(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	if tx == nil {
		return errors.New("reset publication planning: DBTX is nil")
	}
	transfer, err := lockTransferByID(ctx, tx, transferID)
	if err != nil {
		return err
	}
	if transfer.Phase != transfer_service.PhaseAwaitingAuthorization ||
		transfer.Outcome != transfer_service.OutcomeRunning {
		return transfer_service.ErrPreparationResultConflict
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
			AND group_target.preparation_status = 'succeeded';
	`
	if _, err := tx.Exec(ctx, resetItems, transferID); err != nil {
		return fmt.Errorf("reset publication item projections: %w", err)
	}
	const resetGroups = `
		UPDATE wb.transfer_group_targets
		SET publication_status = 'not_started',
		    media_status = 'not_started',
		    overall_outcome = 'running',
		    attention_code = NULL,
		    finished_at = NULL,
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND preparation_status = 'succeeded';
	`
	if _, err := tx.Exec(ctx, resetGroups, transferID); err != nil {
		return fmt.Errorf("reset publication group projections: %w", err)
	}
	const touchTransfer = `
		UPDATE wb.transfers
		SET revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND phase = 'awaiting_authorization'
			AND outcome = 'running';
	`
	result, err := tx.Exec(ctx, touchTransfer, transferID)
	if err != nil {
		return fmt.Errorf("reset transfer publication planning: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

var _ transfer_service.PublicationExecutionResultRepository = (*Repository)(nil)
