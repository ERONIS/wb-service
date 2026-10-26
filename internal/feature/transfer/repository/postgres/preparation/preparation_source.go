package transfer_postgres_preparation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	transfer_postgres_workflow "github.com/ERONIS/wb-service/internal/feature/transfer/repository/postgres/workflow"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

var _ transfer_preparation.TransferRepository = (*PreparationRepository)(nil)

func (repository *PreparationRepository) ListPreparing(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
) ([]transfer_model.Transfer, error) {
	const condition = `transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS pending_group
				WHERE pending_group.transfer_id = transfer.id
				  AND pending_group.preparation_status = 'running'
			)`
	return repository.transfers.ListTransfers(ctx, afterID, limit, condition, "preparing")
}

// ListPendingPreparationGroups выбирает порцию целевых групп переноса, ожидающих подготовки.
func (repository *PreparationRepository) ListPendingPreparationGroups(
	ctx context.Context,
	transferID transfer_model.TransferID,
	limit int,
	after transfer_preparation.PreparationPendingGroup,
) ([]transfer_preparation.PreparationPendingGroup, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return nil, err
	}
	if transferID <= 0 || limit <= 0 || limit > transfer_postgres_workflow.MaxTransferPageSize || after.SourceGroupID < 0 || after.TargetID < 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT group_target.id, group_target.source_group_id, group_target.target_id
		FROM wb.transfer_group_targets AS group_target
        WHERE group_target.transfer_id = $1
          AND group_target.preparation_status = 'running'
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
		if err := rows.Scan(&g.GroupTargetID, &g.SourceGroupID, &g.TargetID); err != nil {
			return nil, fmt.Errorf("scan pending transfer preparation group: %w", err)
		}
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

func (repository *PreparationRepository) LoadPreparationSource(
	ctx context.Context,
	transferID transfer_model.TransferID,
	groupTargetID int64,
) (transfer_preparation.PreparationSource, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return transfer_preparation.PreparationSource{}, err
	}
	if transferID <= 0 || groupTargetID <= 0 {
		return transfer_preparation.PreparationSource{}, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		SELECT
			target.seller_id,
			item.position,
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
			AND transfer.phase = 'active'
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

	source := transfer_preparation.PreparationSource{}
	for rows.Next() {
		var (
			sellerID string
			item     transfer_preparation.PreparationSourceItem
			payload  []byte
		)
		if err := rows.Scan(
			&sellerID,
			&item.Position,
			&payload,
		); err != nil {
			return transfer_preparation.PreparationSource{}, fmt.Errorf(
				"scan transfer preparation source: %w",
				err,
			)
		}
		if len(source.Items) == 0 {
			source.SellerID = core_domain.SellerID(sellerID)
		} else if source.SellerID != core_domain.SellerID(sellerID) {
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
