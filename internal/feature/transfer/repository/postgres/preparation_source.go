package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"
)

func (repository *Repository) ListPreparing(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
) ([]transfer_model.Transfer, error) {
	const condition = `transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS pending_group
				WHERE pending_group.transfer_id = transfer.id
				  AND pending_group.preparation_status IN ('not_started', 'running')
			)`
	return repository.listTransfers(ctx, afterID, limit, condition, "preparing")
}

func (repository *Repository) BeginPreparation(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
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
			),
			(
				SELECT COUNT(*)
				FROM wb.transfer_item_targets AS item_target
				WHERE item_target.transfer_id = $1
					AND item_target.state IN ('running', 'terminal')
			)
		FROM wb.transfer_group_targets
		WHERE transfer_id = $1;
	`
	var total, active, totalItems, activeItems int64
	if err := tx.QueryRow(ctx, validate, transferID).Scan(
		&total,
		&active,
		&totalItems,
		&activeItems,
	); err != nil {
		return fmt.Errorf("validate transfer preparation activation: %w", err)
	}
	if !validPreparationActivation(
		transfer.GroupTargetsCount,
		transfer.ItemTargetsCount,
		total,
		active,
		totalItems,
		activeItems,
	) {
		return transfer_service.ErrPreparationResultConflict
	}
	return nil
}

func validPreparationActivation(
	expectedGroups int64,
	expectedItems int64,
	totalGroups int64,
	activeGroups int64,
	totalItems int64,
	activeItems int64,
) bool {
	return totalGroups == expectedGroups &&
		activeGroups == totalGroups &&
		totalItems == expectedItems &&
		activeItems == totalItems
}

// ListPendingPreparationGroups выбирает порцию целевых групп переноса в статусе running для выполнения подготовки.
func (repository *Repository) ListPendingPreparationGroups(
	ctx context.Context,
	transferID transfer_model.TransferID,
	limit int,
	after transfer_preparation.PreparationPendingGroup,
) ([]transfer_preparation.PreparationPendingGroup, error) {
	if transferID <= 0 || limit <= 0 || after.SourceGroupID < 0 || after.TargetID < 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
        SELECT group_target.id, group_target.source_group_id, group_target.target_id, target.seller_id
        FROM wb.transfer_group_targets AS group_target
        JOIN wb.transfer_targets AS target
          ON target.transfer_id = group_target.transfer_id AND target.id = group_target.target_id
        WHERE group_target.transfer_id = $1
          AND group_target.preparation_status = 'running'
          AND group_target.preparation_result_revision = 0
          AND (group_target.source_group_id, group_target.target_id) > ($3::bigint, $4::bigint)
        ORDER BY group_target.source_group_id, group_target.target_id
        LIMIT $2;
    `
	rows, err := repository.pool.Query(ctx, query, transferID, limit, after.SourceGroupID, after.TargetID)
	if err != nil {
		return nil, fmt.Errorf("list pending transfer preparation groups: %w", err)
	}
	defer rows.Close()

	groups := make([]transfer_preparation.PreparationPendingGroup, 0)
	for rows.Next() {
		var g transfer_preparation.PreparationPendingGroup
		var cab string
		if err := rows.Scan(&g.GroupTargetID, &g.SourceGroupID, &g.TargetID, &cab); err != nil {
			return nil, fmt.Errorf("scan pending transfer preparation group: %w", err)
		}
		g.SellerID = core_domain.SellerID(cab)
		if err := g.Validate(); err != nil {
			return nil, fmt.Errorf("validate pending transfer preparation group: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending transfer preparation groups: %w", err)
	}
	return groups, nil
}

func (repository *Repository) LoadPreparationSource(
	ctx context.Context,
	transferID transfer_model.TransferID,
	groupTargetID int64,
) (transfer_preparation.PreparationSource, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT
			group_target.source_group_id,
			group_target.target_id,
			target.seller_id,
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
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND group_target.id = $2
			AND group_target.preparation_status = 'running'
		ORDER BY item.position;
	`
	rows, err := repository.pool.Query(ctx, query, transferID, groupTargetID)
	if err != nil {
		return transfer_preparation.PreparationSource{}, fmt.Errorf(
			"query transfer preparation source: %w",
			err,
		)
	}
	defer rows.Close()

	source := transfer_preparation.PreparationSource{
		TransferID:    transferID,
		GroupTargetID: groupTargetID,
	}
	for rows.Next() {
		var (
			sourceGroupID int64
			targetID      int64
			sellerID      string
			item          transfer_preparation.PreparationSourceItem
			payload       []byte
		)
		if err := rows.Scan(
			&sourceGroupID,
			&targetID,
			&sellerID,
			&item.TransferItemID,
			&item.Position,
			&item.VendorCode,
			&payload,
		); err != nil {
			return transfer_preparation.PreparationSource{}, fmt.Errorf(
				"scan transfer preparation source: %w",
				err,
			)
		}
		if len(source.Items) == 0 {
			source.SourceGroupID = sourceGroupID
			source.TargetID = targetID
			source.SellerID = core_domain.SellerID(sellerID)
		} else if source.SourceGroupID != sourceGroupID ||
			source.TargetID != targetID ||
			source.SellerID != core_domain.SellerID(sellerID) {
			return transfer_preparation.PreparationSource{}, errors.New(
				"transfer preparation source identity changed within result",
			)
		}
		if err := json.Unmarshal(payload, &item.Card); err != nil {
			return transfer_preparation.PreparationSource{}, fmt.Errorf(
				"decode transfer preparation source payload: %w",
				err,
			)
		}
		source.Items = append(source.Items, item)
	}
	if err := rows.Err(); err != nil {
		return transfer_preparation.PreparationSource{}, fmt.Errorf(
			"iterate transfer preparation source: %w",
			err,
		)
	}
	if len(source.Items) == 0 {
		return transfer_preparation.PreparationSource{}, fmt.Errorf(
			"transfer preparation source ID='%d/%d': %w",
			transferID,
			groupTargetID,
			core_errors.ErrNotFound,
		)
	}
	if err := source.Validate(); err != nil {
		return transfer_preparation.PreparationSource{}, err
	}
	return source, nil
}

var _ transfer_workflow.Repository = (*Repository)(nil)
