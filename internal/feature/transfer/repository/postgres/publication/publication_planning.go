package transfer_postgres_publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

const publicationPlanningWaveGroupLimit = 30

type publicationPlanningMemberRow struct {
	TransferItemTargetID *int64          `json:"transferItemTargetID"`
	Position             int             `json:"position"`
	VendorCode           string          `json:"vendorCode"`
	Payload              json.RawMessage `json:"payload"`
}

func (repository *PublicationRepository) ListReadyForPublicationPlanning(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
) ([]transfer_model.Transfer, error) {
	const condition = `transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND EXISTS (
				SELECT 1
				FROM wb.transfer_group_targets AS ready_group
				WHERE ready_group.transfer_id = transfer.id
				  AND ready_group.preparation_status = 'succeeded'
				  AND ready_group.preparation_result_code = 'prepared'
				  AND ready_group.publication_status = 'not_started'
				  AND ready_group.publication_plan_id IS NULL
			)`
	return repository.transfers.ListTransfers(ctx, afterID, limit, condition, "ready-for-publication-planning")
}

func (repository *PublicationRepository) LoadPublicationPlanningSource(
	ctx context.Context,
	transferID transfer_model.TransferID,
) (transfer_publication.PublicationPlanningSource, error) {
	if err := core_errors.RequireContext(ctx); err != nil {
		return transfer_publication.PublicationPlanningSource{}, err
	}
	if transferID <= 0 {
		return transfer_publication.PublicationPlanningSource{}, core_errors.ErrInvalidArgument
	}
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
			group_target.target_id,
			target.seller_id,
			artifact.group_target_id IS NOT NULL,
			artifact.subject_id,
			artifact.request_payload,
			artifact.limits_free,
			artifact.limits_paid,
			artifact.limits_observed_at,
			artifact.metadata_snapshot,
			members.item_count,
			members.ready_count,
			members.payload
		FROM wb.transfers AS transfer
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = transfer.id
		JOIN ready_groups AS ready_group
		  ON ready_group.id = group_target.id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = group_target.transfer_id
		 AND target.id = group_target.target_id
		LEFT JOIN wb.card_preparation_artifacts AS artifact
		  ON artifact.transfer_id = group_target.transfer_id
		 AND artifact.group_target_id = group_target.id
		JOIN LATERAL (
			SELECT
				COUNT(*) AS item_count,
				COUNT(item_target.id) AS ready_count,
				COALESCE(
					JSONB_AGG(JSONB_BUILD_OBJECT(
						'transferItemTargetID', item_target.id,
						'position', item.position,
						'vendorCode', item.vendor_code,
						'payload', item.payload
					) ORDER BY item.position),
					'[]'::jsonb
				) AS payload
			FROM wb.transfer_items AS item
			LEFT JOIN wb.transfer_item_targets AS item_target
			  ON item_target.transfer_id = group_target.transfer_id
			 AND item_target.group_target_id = group_target.id
			 AND item_target.transfer_item_id = item.id
			 AND item_target.state = 'running'
			 AND item_target.source_action_id IS NULL
			WHERE item.transfer_id = group_target.transfer_id
			  AND item.source_group_id = group_target.source_group_id
		) AS members ON true
		WHERE transfer.id = $1
			AND transfer.phase = 'active'
			AND transfer.outcome = 'running'
			AND group_target.preparation_status = 'succeeded'
			AND group_target.preparation_result_code = 'prepared'
			AND group_target.publication_status = 'not_started'
			AND group_target.publication_plan_id IS NULL
		ORDER BY target.position, group_target.id;
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
	for rows.Next() {
		var (
			group            transfer_publication.PublicationPlanningGroup
			sellerID         string
			artifactPresent  bool
			subjectID        *int64
			limitsFree       *int64
			limitsPaid       *int64
			limitsObservedAt *time.Time
			requestPayload   []byte
			snapshotPayload  []byte
			membersPayload   []byte
			itemCount        int64
			readyCount       int64
		)
		if err := rows.Scan(
			&group.GroupTargetID,
			&group.TargetID,
			&sellerID,
			&artifactPresent,
			&subjectID,
			&requestPayload,
			&limitsFree,
			&limitsPaid,
			&limitsObservedAt,
			&snapshotPayload,
			&itemCount,
			&readyCount,
			&membersPayload,
		); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"scan publication planning source: %w",
				err,
			)
		}
		if !artifactPresent {
			return transfer_publication.PublicationPlanningSource{}, transfer_preparation.ErrProposalNotFound
		}
		if subjectID == nil || limitsFree == nil || limitsPaid == nil || limitsObservedAt == nil ||
			itemCount == 0 || itemCount != readyCount {
			return transfer_publication.PublicationPlanningSource{}, transfer_preparation.ErrPreparationMismatch
		}
		group.SellerID = core_domain.SellerID(sellerID)
		group.Proposal.SubjectID = *subjectID
		group.Proposal.Limits.FreeLimits = *limitsFree
		group.Proposal.Limits.PaidLimits = *limitsPaid
		group.Proposal.LimitsObservedAt = limitsObservedAt.UTC()
		if err := json.Unmarshal(requestPayload, &group.Proposal.Request); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"decode card preparation request: %w",
				err,
			)
		}
		group.Proposal.EncodedRequest = append([]byte(nil), requestPayload...)
		if err := json.Unmarshal(snapshotPayload, &group.Proposal.MetadataSnapshot); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"decode card preparation metadata snapshot: %w",
				err,
			)
		}
		var members []publicationPlanningMemberRow
		if err := json.Unmarshal(membersPayload, &members); err != nil {
			return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
				"decode publication planning members: %w",
				err,
			)
		}
		if int64(len(members)) != itemCount {
			return transfer_publication.PublicationPlanningSource{}, transfer_preparation.ErrPreparationMismatch
		}
		group.Members = make([]transfer_publication.PublicationPlanningMember, len(members))
		for index, member := range members {
			if member.TransferItemTargetID == nil {
				return transfer_publication.PublicationPlanningSource{}, transfer_preparation.ErrPreparationMismatch
			}
			var card cardpipeline.Card
			if err := json.Unmarshal(member.Payload, &card); err != nil {
				return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
					"decode publication planning item payload: %w",
					err,
				)
			}
			group.Members[index] = transfer_publication.PublicationPlanningMember{
				TransferItemTargetID: *member.TransferItemTargetID,
				Position:             member.Position,
				VendorCode:           member.VendorCode,
				Media:                card.Media,
			}
		}
		source.Groups = append(source.Groups, group)
	}
	if err := rows.Err(); err != nil {
		return transfer_publication.PublicationPlanningSource{}, fmt.Errorf(
			"iterate publication planning source: %w",
			err,
		)
	}

	if err := source.Validate(); err != nil {
		return transfer_publication.PublicationPlanningSource{}, errors.Join(
			transfer_preparation.ErrPreparationMismatch,
			err,
		)
	}
	return source, nil
}
