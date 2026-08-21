package cardprepare_postgres_repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) StartPreparation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardprepare_service.StartPreparationCommand,
) error {
	if tx == nil {
		return errors.New("start card preparation: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	expectedGroupTargets := int64(command.GroupsCount) * int64(command.TargetsCount)

	const insertPreparation = `
		INSERT INTO wb.card_preparations (
			transfer_id,
			expected_group_targets,
			batch_schema_version,
			batch_normalization_version,
			batch_checksum
		)
		SELECT
			transfer.id,
			$9,
			transfer.batch_schema_version,
			transfer.batch_normalization_version,
			transfer.batch_checksum
		FROM wb.transfers AS transfer
		WHERE transfer.id = $1
			AND transfer.batch_id = $2
			AND transfer.phase = 'preparing'
			AND transfer.outcome = 'running'
			AND transfer.batch_schema_version = $3
			AND transfer.batch_normalization_version = $4
			AND transfer.items_count = $5
			AND transfer.groups_count = $6
			AND transfer.targets_count = $7
			AND transfer.batch_checksum = $8
			AND transfer.group_targets_count = $9
		ON CONFLICT (transfer_id) DO NOTHING;
	`
	if _, err := tx.Exec(
		ctx,
		insertPreparation,
		command.TransferID,
		command.BatchID,
		command.BatchSchemaVersion,
		command.NormalizationVersion,
		command.ItemsCount,
		command.GroupsCount,
		command.TargetsCount,
		command.BatchChecksum[:],
		expectedGroupTargets,
	); err != nil {
		return fmt.Errorf("insert card preparation: %w", err)
	}

	const loadPreparation = `
		SELECT
			id,
			expected_group_targets,
			batch_schema_version,
			batch_normalization_version,
			batch_checksum
		FROM wb.card_preparations
		WHERE transfer_id = $1
		FOR UPDATE;
	`
	var preparationID int64
	var storedGroupTargets int64
	var storedSchemaVersion int
	var storedNormalizationVersion int
	var storedChecksum []byte
	if err := tx.QueryRow(ctx, loadPreparation, command.TransferID).Scan(
		&preparationID,
		&storedGroupTargets,
		&storedSchemaVersion,
		&storedNormalizationVersion,
		&storedChecksum,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardprepare_service.ErrPreparationMismatch
		}
		return fmt.Errorf("load card preparation: %w", err)
	}
	if storedGroupTargets != expectedGroupTargets ||
		storedSchemaVersion != command.BatchSchemaVersion ||
		storedNormalizationVersion != command.NormalizationVersion ||
		!bytes.Equal(storedChecksum, command.BatchChecksum[:]) {
		return cardprepare_service.ErrPreparationMismatch
	}

	const fanout = `
		INSERT INTO wb.card_preparation_groups (
			preparation_id,
			transfer_id,
			group_target_id,
			source_group_id,
			target_id,
			cabinet_id
		)
		SELECT
			$2,
			group_target.transfer_id,
			group_target.id,
			group_target.source_group_id,
			group_target.target_id,
			target.cabinet_id
		FROM wb.transfer_group_targets AS group_target
		JOIN wb.transfer_targets AS target
			ON target.transfer_id = group_target.transfer_id
		   AND target.id = group_target.target_id
		WHERE group_target.transfer_id = $1
		ON CONFLICT (transfer_id, group_target_id) DO NOTHING;
	`
	if _, err := tx.Exec(ctx, fanout, command.TransferID, preparationID); err != nil {
		return fmt.Errorf("fan out card preparation: %w", err)
	}

	const validateFanout = `
		SELECT COUNT(*)
		FROM wb.card_preparation_groups
		WHERE transfer_id = $1 AND preparation_id = $2;
	`
	var storedCount int64
	if err := tx.QueryRow(ctx, validateFanout, command.TransferID, preparationID).Scan(&storedCount); err != nil {
		return fmt.Errorf("count card preparation work: %w", err)
	}
	if storedCount != expectedGroupTargets {
		return cardprepare_service.ErrPreparationMismatch
	}

	const markProcessing = `
		UPDATE wb.card_preparations
		SET status = 'processing',
		    started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND transfer_id = $2 AND status = 'pending';
	`
	if _, err := tx.Exec(ctx, markProcessing, preparationID, command.TransferID); err != nil {
		return fmt.Errorf("mark card preparation processing: %w", err)
	}
	return nil
}
