package transfer_postgres_repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) ApplyPreparationResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPreparationResultCommand,
) error {
	if tx == nil {
		return errors.New("apply preparation result: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	transferPhase, err := lockTransferForPipelineResult(ctx, tx, command.TransferID)
	if err != nil {
		return err
	}
	var proposalRoot []byte
	if command.Status == transfer_service.PreparationResultSucceeded {
		proposalRoot = command.ProposalRoot[:]
	}
	if transferPhase != "preparing" {
		return ensureSamePreparationResult(ctx, tx, command)
	}

	const updateProjection = `
		UPDATE wb.transfer_group_targets AS group_target
		SET
			preparation_status = $3,
			preparation_result_revision = $4,
			preparation_id = $5,
			preparation_group_id = $6,
			preparation_result_code = $7,
			preparation_proposal_root = $8,
			overall_outcome = CASE $3
				WHEN 'succeeded' THEN 'running'
				WHEN 'rejected' THEN 'rejected'
				WHEN 'unresolved' THEN 'unresolved'
			END,
			attention_code = CASE
				WHEN $3 = 'unresolved' THEN $7
				ELSE NULL
			END,
			finished_at = CASE
				WHEN $3 IN ('rejected', 'unresolved') THEN CURRENT_TIMESTAMP
				ELSE NULL
			END,
			revision = group_target.revision + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE group_target.transfer_id = $1
			AND group_target.id = $2
			AND group_target.preparation_status = 'running'
			AND group_target.preparation_result_revision = 0
			AND EXISTS (
				SELECT 1
				FROM wb.transfers AS transfer
				WHERE transfer.id = group_target.transfer_id
					AND transfer.phase = 'preparing'
					AND transfer.outcome = 'running'
			);
	`
	result, err := tx.Exec(
		ctx,
		updateProjection,
		command.TransferID,
		command.GroupTargetID,
		command.Status,
		command.Revision,
		command.PreparationID,
		command.PreparationGroupID,
		command.OutcomeCode,
		proposalRoot,
	)
	if err != nil {
		return fmt.Errorf("update transfer preparation result: %w", err)
	}
	if result.RowsAffected() == 0 {
		if err := ensureSamePreparationResult(ctx, tx, command); err != nil {
			return err
		}
	}

	if command.Status != transfer_service.PreparationResultSucceeded {
		const finishItems = `
			UPDATE wb.transfer_item_targets
			SET state = 'terminal',
			    outcome_class = CASE $3
					WHEN 'rejected' THEN 'rejected'
					WHEN 'unresolved' THEN 'unresolved'
				END,
			    outcome_code = $4,
			    finished_at = CURRENT_TIMESTAMP,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE transfer_id = $1
				AND group_target_id = $2
				AND state = 'running';
		`
		if _, err := tx.Exec(
			ctx,
			finishItems,
			command.TransferID,
			command.GroupTargetID,
			command.Status,
			command.OutcomeCode,
		); err != nil {
			return fmt.Errorf("finish transfer preparation items: %w", err)
		}
	}

	const advanceTransfer = `
		WITH counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (
					WHERE preparation_status IN ('succeeded', 'rejected', 'unresolved')
				) AS terminal
			FROM wb.transfer_group_targets
			WHERE transfer_id = $1
		)
		UPDATE wb.transfers AS transfer
		SET
			phase = 'awaiting_authorization',
			revision = transfer.revision + 1,
			updated_at = CURRENT_TIMESTAMP
		FROM counts
		WHERE transfer.id = $1
			AND transfer.phase = 'preparing'
			AND transfer.outcome = 'running'
			AND counts.total = transfer.group_targets_count
			AND counts.terminal = counts.total;
	`
	if _, err := tx.Exec(ctx, advanceTransfer, command.TransferID); err != nil {
		return fmt.Errorf("advance transfer after preparation: %w", err)
	}
	return nil
}

func lockTransferForPipelineResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) (string, error) {
	const query = `
		SELECT phase, outcome
		FROM wb.transfers
		WHERE id = $1
		FOR UPDATE;
	`
	var phase, outcome string
	if err := tx.QueryRow(ctx, query, transferID).Scan(&phase, &outcome); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", transfer_service.ErrPreparationResultConflict
		}
		return "", fmt.Errorf("lock transfer pipeline projection: %w", err)
	}
	if phase == "initializing" || outcome != "running" {
		return "", transfer_service.ErrPreparationResultConflict
	}
	return phase, nil
}

func ensureSamePreparationResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command transfer_service.ApplyPreparationResultCommand,
) error {
	const load = `
		SELECT
			preparation_status,
			preparation_result_revision,
			COALESCE(preparation_id, 0),
			COALESCE(preparation_group_id, 0),
			COALESCE(preparation_result_code, ''),
			preparation_proposal_root
		FROM wb.transfer_group_targets
		WHERE transfer_id = $1
			AND id = $2
		FOR UPDATE;
	`
	var (
		status             string
		revision           int64
		preparationID      int64
		preparationGroupID int64
		outcomeCode        string
		proposalRoot       []byte
	)
	err := tx.QueryRow(
		ctx,
		load,
		command.TransferID,
		command.GroupTargetID,
	).Scan(
		&status,
		&revision,
		&preparationID,
		&preparationGroupID,
		&outcomeCode,
		&proposalRoot,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.ErrPreparationResultConflict
	}
	if err != nil {
		return fmt.Errorf("load transfer preparation result: %w", err)
	}
	var expectedRoot []byte
	if command.Status == transfer_service.PreparationResultSucceeded {
		expectedRoot = command.ProposalRoot[:]
	}
	if status != string(command.Status) || revision != command.Revision ||
		preparationID != command.PreparationID ||
		preparationGroupID != command.PreparationGroupID ||
		outcomeCode != command.OutcomeCode ||
		!bytes.Equal(proposalRoot, expectedRoot) {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

var _ transfer_service.PreparationResultRepository = (*Repository)(nil)
