package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) ListAwaitingAuthorization(
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
		WHERE transfer.phase = 'awaiting_authorization'
			AND transfer.outcome = 'running'
			AND transfer.id > $1
		ORDER BY transfer.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list awaiting-authorization transfers: %w", err)
	}
	defer rows.Close()

	transfers := make([]transfer_service.Transfer, 0)
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan awaiting-authorization transfer: %w", err)
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate awaiting-authorization transfers: %w", err)
	}
	return transfers, nil
}

func (repository *Repository) LoadPublicationPlanningSource(
	ctx context.Context,
	transferID transfer_service.TransferID,
) (transfer_service.PublicationPlanningSource, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()

	const query = `
		SELECT
			group_target.id,
			group_target.source_group_id,
			group_target.target_id,
			target.cabinet_id,
			group_target.preparation_group_id,
			group_target.preparation_proposal_root,
			item.id,
			item_target.id,
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
		JOIN wb.transfer_item_targets AS item_target
		  ON item_target.transfer_id = group_target.transfer_id
		 AND item_target.group_target_id = group_target.id
		 AND item_target.transfer_item_id = item.id
		WHERE transfer.id = $1
			AND transfer.phase = 'awaiting_authorization'
			AND transfer.outcome = 'running'
			AND group_target.preparation_status = 'succeeded'
			AND group_target.preparation_result_code = 'prepared'
			AND group_target.preparation_group_id IS NOT NULL
			AND group_target.preparation_proposal_root IS NOT NULL
			AND item_target.state = 'running'
		ORDER BY target.position, group_target.id, item.position;
	`
	rows, err := repository.pool.Query(ctx, query, transferID)
	if err != nil {
		return transfer_service.PublicationPlanningSource{}, fmt.Errorf(
			"query publication planning source: %w",
			err,
		)
	}
	defer rows.Close()

	source := transfer_service.PublicationPlanningSource{TransferID: transferID}
	var current *transfer_service.PublicationPlanningGroup
	for rows.Next() {
		var (
			groupTargetID      int64
			sourceGroupID      int64
			targetID           int64
			cabinetID          string
			preparationGroupID int64
			proposalRoot       []byte
			member             transfer_service.PublicationPlanningMember
			payload            []byte
		)
		if err := rows.Scan(
			&groupTargetID,
			&sourceGroupID,
			&targetID,
			&cabinetID,
			&preparationGroupID,
			&proposalRoot,
			&member.TransferItemID,
			&member.TransferItemTargetID,
			&member.Position,
			&member.VendorCode,
			&payload,
		); err != nil {
			return transfer_service.PublicationPlanningSource{}, fmt.Errorf(
				"scan publication planning source: %w",
				err,
			)
		}
		if current == nil || current.GroupTargetID != groupTargetID {
			var root transfer_service.Digest
			if len(proposalRoot) != len(root) {
				return transfer_service.PublicationPlanningSource{}, errors.New(
					"publication planning proposal root has invalid length",
				)
			}
			copy(root[:], proposalRoot)
			source.Groups = append(source.Groups, transfer_service.PublicationPlanningGroup{
				GroupTargetID:      groupTargetID,
				SourceGroupID:      sourceGroupID,
				TargetID:           targetID,
				CabinetID:          transfer_service.CabinetID(cabinetID),
				PreparationGroupID: preparationGroupID,
				ProposalRoot:       root,
			})
			current = &source.Groups[len(source.Groups)-1]
		} else if current.SourceGroupID != sourceGroupID ||
			current.TargetID != targetID ||
			current.CabinetID != transfer_service.CabinetID(cabinetID) ||
			current.PreparationGroupID != preparationGroupID {
			return transfer_service.PublicationPlanningSource{}, errors.New(
				"publication planning group identity changed within result",
			)
		}
		var card cardimport_service.AggregatedCard
		if err := json.Unmarshal(payload, &card); err != nil {
			return transfer_service.PublicationPlanningSource{}, fmt.Errorf(
				"decode publication planning item payload: %w",
				err,
			)
		}
		member.Media = card.Media
		current.Members = append(current.Members, member)
	}
	if err := rows.Err(); err != nil {
		return transfer_service.PublicationPlanningSource{}, fmt.Errorf(
			"iterate publication planning source: %w",
			err,
		)
	}

	// An empty source is valid when preparation rejected or could not resolve
	// every group-target. The planner will still persist a zero-action plan and
	// let the transfer reducer finish the already-terminal item results.
	if err := source.Validate(); err != nil {
		return transfer_service.PublicationPlanningSource{}, err
	}
	return source, nil
}
