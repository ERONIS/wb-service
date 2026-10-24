package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

const publicationPlanningWaveGroupLimit = 30

func (repository *Repository) ListAwaitingAuthorization(
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
				FROM wb.transfer_group_targets AS ready_group
				WHERE ready_group.transfer_id = transfer.id
				  AND ready_group.preparation_status = 'succeeded'
				  AND ready_group.publication_status = 'not_started'
				  AND ready_group.publication_plan_id IS NULL
			)`
	return repository.listTransfers(ctx, afterID, limit, condition, "awaiting-authorization")
}

func (repository *Repository) LoadPublicationPlanningSource(
	ctx context.Context,
	transferID transfer_model.TransferID,
) (transfer_publication.PublicationPlanningSource, error) {
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const query = `
		WITH ready_groups AS (
			SELECT group_target.id
			FROM wb.transfer_group_targets AS group_target
			JOIN wb.transfer_targets AS target
			  ON target.transfer_id = group_target.transfer_id
			 AND target.id = group_target.target_id
			WHERE group_target.transfer_id = $1
			  AND group_target.preparation_status = 'succeeded'
			  AND group_target.preparation_result_code = 'prepared'
			  AND group_target.publication_status = 'not_started'
			  AND group_target.publication_plan_id IS NULL
			ORDER BY target.position, group_target.id
			LIMIT $2
		)
		SELECT
			group_target.id,
			group_target.source_group_id,
			group_target.target_id,
			target.seller_id,
			item.id,
			item_target.id,
			item.position,
			item.vendor_code,
			item.payload
		FROM wb.transfers AS transfer
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = transfer.id
		JOIN ready_groups AS ready_group
		  ON ready_group.id = group_target.id
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
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND group_target.preparation_status = 'succeeded'
			AND group_target.preparation_result_code = 'prepared'
			AND group_target.publication_status = 'not_started'
			AND group_target.publication_plan_id IS NULL
			AND item_target.state = 'running'
			AND item_target.source_action_id IS NULL
		ORDER BY target.position, group_target.id, item.position;
	`
	rows, err := repository.pool.Query(
		ctx,
		query,
		transferID,
		publicationPlanningWaveGroupLimit,
	)
	if err != nil {
		return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
			"query publication planning source: %w",
			err,
		)
	}
	defer rows.Close()

	source := transfer_publication.PublicationPlanningSource{TransferID: transferID}
	var current *transfer_publication.PublicationPlanningGroup
	for rows.Next() {
		var (
			groupTargetID int64
			sourceGroupID int64
			targetID      int64
			sellerID      string
			member        transfer_publication.PublicationPlanningMember
			payload       []byte
		)
		if err := rows.Scan(
			&groupTargetID,
			&sourceGroupID,
			&targetID,
			&sellerID,
			&member.TransferItemID,
			&member.TransferItemTargetID,
			&member.Position,
			&member.VendorCode,
			&payload,
		); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"scan publication planning source: %w",
				err,
			)
		}
		if current == nil || current.GroupTargetID != groupTargetID {
			source.Groups = append(source.Groups, transfer_publication.PublicationPlanningGroup{
				GroupTargetID: groupTargetID,
				SourceGroupID: sourceGroupID,
				TargetID:      targetID,
				SellerID:      core_domain.SellerID(sellerID),
			})
			current = &source.Groups[len(source.Groups)-1]
		} else if current.SourceGroupID != sourceGroupID ||
			current.TargetID != targetID ||
			current.SellerID != core_domain.SellerID(sellerID) {
			return transfer_publication.PublicationPlanningSource{}, errors.New(
				"publication planning group identity changed within result",
			)
		}
		var card cardpipeline.Card
		if err := json.Unmarshal(payload, &card); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"decode publication planning item payload: %w",
				err,
			)
		}
		member.Media = card.Media
		current.Members = append(current.Members, member)
	}
	if err := rows.Err(); err != nil {
		return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
			"iterate publication planning source: %w",
			err,
		)
	}

	if err := source.Validate(); err != nil {
		return transfer_publication.PublicationPlanningSource{}, err
	}
	return source, nil
}
