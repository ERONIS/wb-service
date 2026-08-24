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
		transfer.Phase != transfer_service.PhaseAwaitingAuthorization {
		return transfer_service.ErrPreparationResultConflict
	}

	for _, group := range command.Groups {
		publicationStatus := string(group.Status)
		mediaStatus := "skipped"
		overallOutcome := string(group.OutcomeClass)
		var finished bool
		if group.Status == transfer_service.PublicationProjectionRunning {
			if group.HasMediaAction {
				mediaStatus = "running"
			}
			overallOutcome = "running"
			finished = false
		} else {
			finished = true
		}

		const updateGroup = `
			UPDATE wb.transfer_group_targets
			SET publication_status = $3,
			    media_status = $4,
			    overall_outcome = $5,
			    attention_code = NULL,
			    finished_at = CASE WHEN $6 THEN CURRENT_TIMESTAMP ELSE NULL END,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND id = $2
				AND preparation_status = 'succeeded'
				AND publication_status = 'not_started'
				AND overall_outcome = 'running';
		`
		result, err := tx.Exec(
			ctx,
			updateGroup,
			command.TransferID,
			group.GroupTargetID,
			publicationStatus,
			mediaStatus,
			overallOutcome,
			finished,
		)
		if err != nil {
			return fmt.Errorf("update publication group projection: %w", err)
		}
		if result.RowsAffected() != 1 {
			return transfer_service.ErrPreparationResultConflict
		}
	}

	for _, item := range command.Items {
		if item.SourceActionID > 0 {
			const attachAction = `
				UPDATE wb.transfer_item_targets
				SET source_action_id = $4,
				    revision = revision + 1,
				    updated_at = CURRENT_TIMESTAMP
				WHERE transfer_id = $1
					AND group_target_id = $2
					AND id = $3
					AND state = 'running'
					AND source_action_id IS NULL;
			`
			result, err := tx.Exec(
				ctx,
				attachAction,
				command.TransferID,
				item.GroupTargetID,
				item.TransferItemTargetID,
				item.SourceActionID,
			)
			if err != nil {
				return fmt.Errorf("attach publication action to item projection: %w", err)
			}
			if result.RowsAffected() != 1 {
				return transfer_service.ErrPreparationResultConflict
			}
			continue
		}

		var nmID any
		if item.NMID > 0 {
			nmID = item.NMID
		}
		const finishItem = `
			UPDATE wb.transfer_item_targets
			SET state = 'terminal',
			    outcome_class = $4,
			    outcome_code = $5,
			    nm_id = $6,
			    finished_at = CURRENT_TIMESTAMP,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND group_target_id = $2
				AND id = $3
				AND state = 'running'
				AND source_action_id IS NULL;
		`
		result, err := tx.Exec(
			ctx,
			finishItem,
			command.TransferID,
			item.GroupTargetID,
			item.TransferItemTargetID,
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
	}

	const skipFailedPreparationStages = `
		UPDATE wb.transfer_group_targets
		SET publication_status = 'skipped',
		    media_status = 'skipped',
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND preparation_status IN ('rejected', 'unresolved')
			AND publication_status = 'not_started'
			AND media_status = 'not_started';
	`
	if _, err := tx.Exec(ctx, skipFailedPreparationStages, command.TransferID); err != nil {
		return fmt.Errorf("skip publication after preparation result: %w", err)
	}

	const validateCoverage = `
		SELECT
			COUNT(*) FILTER (WHERE preparation_status = 'succeeded'),
			COUNT(*) FILTER (
				WHERE preparation_status = 'succeeded'
				  AND publication_status <> 'not_started'
			),
			COUNT(*) FILTER (WHERE publication_status = 'not_started')
		FROM wb.transfer_group_targets
		WHERE transfer_id = $1;
	`
	var prepared, planned, notStarted int64
	if err := tx.QueryRow(ctx, validateCoverage, command.TransferID).Scan(
		&prepared,
		&planned,
		&notStarted,
	); err != nil {
		return fmt.Errorf("validate publication projection coverage: %w", err)
	}
	if prepared != int64(len(command.Groups)) || planned != prepared || notStarted != 0 {
		return transfer_service.ErrPreparationResultConflict
	}

	if command.ActionCount > 0 {
		const touchTransfer = `
			UPDATE wb.transfers
			SET revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
				AND phase = 'awaiting_authorization'
				AND outcome = 'running';
		`
		if _, err := tx.Exec(ctx, touchTransfer, command.TransferID); err != nil {
			return fmt.Errorf("touch transfer publication plan: %w", err)
		}
		return nil
	}

	const finishTransfer = `
		WITH item_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'terminal') AS terminal,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
			FROM wb.transfer_item_targets
			WHERE transfer_id = $1
		)
		UPDATE wb.transfers AS transfer
		SET phase = 'finished',
		    outcome = CASE
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'unresolved'
				WHEN item_counts.rejected = item_counts.total THEN 'rejected'
				WHEN item_counts.rejected > 0 AND item_counts.accepted > 0 THEN 'partial'
				ELSE 'succeeded'
			END,
		    attention_code = CASE
				WHEN item_counts.unresolved > 0 OR item_counts.internal_error > 0
					THEN 'terminal_item_requires_attention'
				ELSE NULL
			END,
		    finished_at = CURRENT_TIMESTAMP,
		    revision = transfer.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM item_counts
		WHERE transfer.id = $1
			AND transfer.phase = 'awaiting_authorization'
			AND transfer.outcome = 'running'
			AND item_counts.total = transfer.item_targets_count
			AND item_counts.terminal = item_counts.total;
	`
	result, err := tx.Exec(ctx, finishTransfer, command.TransferID)
	if err != nil {
		return fmt.Errorf("finish zero-action transfer: %w", err)
	}
	if result.RowsAffected() != 1 {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

var _ transfer_service.PublicationPlanResultRepository = (*Repository)(nil)
