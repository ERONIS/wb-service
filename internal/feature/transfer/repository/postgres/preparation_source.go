package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) ListPreparing(
	ctx context.Context,
	afterID transfer_service.TransferID,
	limit int,
) ([]transfer_service.Transfer, error) {
	if afterID < 0 || limit <= 0 || limit > initializationListPageLimit {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()

	query := `
		SELECT ` + transferColumns + `
		FROM wb.transfers AS transfer
		WHERE transfer.phase = 'preparing'
			AND transfer.outcome = 'running'
			AND transfer.id > $1
		ORDER BY transfer.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list preparing transfers: %w", err)
	}
	defer rows.Close()

	transfers := make([]transfer_service.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan preparing transfer: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preparing transfers: %w", err)
	}
	return transfers, nil
}

func (repository *Repository) BeginPreparation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) error {
	if tx == nil {
		return errors.New("begin transfer preparation: DBTX is nil")
	}
	transfer, err := lockTransferByID(ctx, tx, transferID)
	if err != nil {
		return err
	}
	if transfer.Phase != transfer_service.PhasePreparing {
		if transfer.Phase == transfer_service.PhaseInitializing {
			return transfer_service.ErrPreparationResultConflict
		}
		return nil
	}

	const activate = `
		UPDATE wb.transfer_group_targets
		SET preparation_status = 'running',
		    started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND preparation_status = 'not_started';
	`
	if _, err := tx.Exec(ctx, activate, transferID); err != nil {
		return fmt.Errorf("activate transfer preparation groups: %w", err)
	}

	const activateItems = `
		UPDATE wb.transfer_item_targets
		SET state = 'running',
		    started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		    revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND state = 'pending';
	`
	if _, err := tx.Exec(ctx, activateItems, transferID); err != nil {
		return fmt.Errorf("activate transfer preparation items: %w", err)
	}

	const validate = `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (
				WHERE preparation_status IN (
					'running', 'succeeded', 'rejected', 'unresolved', 'skipped'
				)
			),
			(
				SELECT COUNT(*)
				FROM wb.transfer_item_targets AS item_target
				WHERE item_target.transfer_id = $1
					AND item_target.state = 'running'
			)
		FROM wb.transfer_group_targets
		WHERE transfer_id = $1;
	`
	var total, active, activeItems int64
	if err := tx.QueryRow(ctx, validate, transferID).Scan(
		&total,
		&active,
		&activeItems,
	); err != nil {
		return fmt.Errorf("validate transfer preparation activation: %w", err)
	}
	if total != transfer.GroupTargetsCount || active != total ||
		activeItems != transfer.ItemTargetsCount {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func (repository *Repository) LoadPreparationSource(
	ctx context.Context,
	transferID transfer_service.TransferID,
	groupTargetID int64,
) (transfer_service.PreparationSource, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()

	const query = `
		SELECT
			group_target.source_group_id,
			group_target.target_id,
			target.cabinet_id,
			item.id,
			item.position,
			item.vendor_code,
			item.payload
		FROM wb.transfers AS transfer
		JOIN wb.transfer_group_targets AS group_target
			ON group_target.transfer_id = transfer.id
		JOIN wb.transfer_targets AS target
			ON target.transfer_id = group_target.transfer_id
		   AND target.id = group_target.target_id
		JOIN wb.transfer_items AS item
			ON item.transfer_id = group_target.transfer_id
		   AND item.source_group_id = group_target.source_group_id
		WHERE transfer.id = $1
			AND transfer.phase = 'preparing'
			AND transfer.outcome = 'running'
			AND group_target.id = $2
			AND group_target.preparation_status = 'running'
		ORDER BY item.position;
	`
	rows, err := repository.pool.Query(ctx, query, transferID, groupTargetID)
	if err != nil {
		return transfer_service.PreparationSource{}, fmt.Errorf(
			"query transfer preparation source: %w",
			err,
		)
	}
	defer rows.Close()

	source := transfer_service.PreparationSource{
		TransferID:    transferID,
		GroupTargetID: groupTargetID,
	}
	for rows.Next() {
		var (
			sourceGroupID int64
			targetID      int64
			cabinetID     string
			item          transfer_service.PreparationSourceItem
			payload       []byte
		)
		if err := rows.Scan(
			&sourceGroupID,
			&targetID,
			&cabinetID,
			&item.TransferItemID,
			&item.Position,
			&item.VendorCode,
			&payload,
		); err != nil {
			return transfer_service.PreparationSource{}, fmt.Errorf(
				"scan transfer preparation source: %w",
				err,
			)
		}
		if len(source.Items) == 0 {
			source.SourceGroupID = sourceGroupID
			source.TargetID = targetID
			source.CabinetID = transfer_service.CabinetID(cabinetID)
		} else if source.SourceGroupID != sourceGroupID ||
			source.TargetID != targetID ||
			source.CabinetID != transfer_service.CabinetID(cabinetID) {
			return transfer_service.PreparationSource{}, errors.New(
				"transfer preparation source identity changed within result",
			)
		}
		if err := json.Unmarshal(payload, &item.Card); err != nil {
			return transfer_service.PreparationSource{}, fmt.Errorf(
				"decode transfer preparation source payload: %w",
				err,
			)
		}
		source.Items = append(source.Items, item)
	}
	if err := rows.Err(); err != nil {
		return transfer_service.PreparationSource{}, fmt.Errorf(
			"iterate transfer preparation source: %w",
			err,
		)
	}
	if len(source.Items) == 0 {
		return transfer_service.PreparationSource{}, fmt.Errorf(
			"transfer preparation source ID='%d/%d': %w",
			transferID,
			groupTargetID,
			core_errors.ErrNotFound,
		)
	}
	if err := source.Validate(); err != nil {
		return transfer_service.PreparationSource{}, err
	}
	return source, nil
}

var _ transfer_service.Repository = (*Repository)(nil)
