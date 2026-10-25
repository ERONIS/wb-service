package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

	"golang.org/x/sync/errgroup"
)

const (
	maxProposalBatchSize      = 100
	proposalDecodeConcurrency = 8
)

type storedProposalRow struct {
	position                       int64
	stored                         transfer_preparation.StoredProposal
	requestPayload                 []byte
	snapshotPayload                []byte
	transferItemIDs, itemPositions []int64
	vendorCodes                    []string
}

func (repository *Repository) LoadProposal(
	ctx context.Context,
	query transfer_preparation.ProposalQuery,
) (transfer_preparation.StoredProposal, error) {
	stored, err := repository.LoadProposals(ctx, []transfer_preparation.ProposalQuery{query})
	if err != nil {
		return transfer_preparation.StoredProposal{}, err
	}
	return stored[0], nil
}

func (repository *Repository) LoadProposals(
	ctx context.Context,
	queries []transfer_preparation.ProposalQuery,
) ([]transfer_preparation.StoredProposal, error) {
	if ctx == nil {
		return nil, errors.New("load card preparation proposals: context is nil")
	}
	if len(queries) == 0 || len(queries) > maxProposalBatchSize {
		return nil, errors.New("card preparation proposal batch size is invalid")
	}
	transferID := queries[0].TransferID
	groupTargetIDs := make([]int64, len(queries))
	for index, query := range queries {
		if err := query.Validate(); err != nil {
			return nil, err
		}
		if query.TransferID != transferID {
			return nil, errors.New("card preparation proposal batch spans transfers")
		}
		groupTargetIDs[index] = query.GroupTargetID
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const load = `
		WITH requested AS (
			SELECT
				request.group_target_id,
				request.position
			FROM UNNEST($2::bigint[])
				WITH ORDINALITY AS request(
					group_target_id,
					position
				)
		)
		SELECT
			request.position,
			group_target.source_group_id,
			group_target.target_id,
			target.seller_id,
			artifact.subject_id,
			artifact.request_payload,
			artifact.limits_free,
			artifact.limits_paid,
			artifact.limits_observed_at,
			artifact.metadata_snapshot,
			members.transfer_item_ids,
			members.item_positions,
			members.vendor_codes
		FROM requested AS request
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = $1
		 AND group_target.id = request.group_target_id
		 AND group_target.preparation_status = 'succeeded'
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = group_target.transfer_id
		 AND target.id = group_target.target_id
		JOIN wb.card_preparation_artifacts AS artifact
		  ON artifact.transfer_id = group_target.transfer_id
		 AND artifact.group_target_id = group_target.id
		JOIN LATERAL (
			SELECT
				ARRAY_AGG(item.id ORDER BY item.position) AS transfer_item_ids,
				ARRAY_AGG(item.position::bigint ORDER BY item.position) AS item_positions,
				ARRAY_AGG(item.vendor_code ORDER BY item.position) AS vendor_codes
			FROM wb.transfer_items AS item
			WHERE item.transfer_id = group_target.transfer_id
			  AND item.source_group_id = group_target.source_group_id
		) AS members ON TRUE
		ORDER BY request.position;
	`
	rows, err := repository.pool.Query(
		ctx,
		load,
		transferID,
		groupTargetIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("query card preparation proposals: %w", err)
	}
	defer rows.Close()

	loadedRows := make([]storedProposalRow, 0, len(queries))
	seen := make([]bool, len(queries))
	for rows.Next() {
		var loaded storedProposalRow
		if err := rows.Scan(
			&loaded.position,
			&loaded.stored.SourceGroupID,
			&loaded.stored.TargetID,
			&loaded.stored.SellerID,
			&loaded.stored.Proposal.SubjectID,
			&loaded.requestPayload,
			&loaded.stored.Proposal.Limits.FreeLimits,
			&loaded.stored.Proposal.Limits.PaidLimits,
			&loaded.stored.Proposal.LimitsObservedAt,
			&loaded.snapshotPayload,
			&loaded.transferItemIDs,
			&loaded.itemPositions,
			&loaded.vendorCodes,
		); err != nil {
			return nil, fmt.Errorf("scan card preparation proposal: %w", err)
		}
		if loaded.position <= 0 || loaded.position > int64(len(queries)) ||
			seen[loaded.position-1] {
			return nil, transfer_preparation.ErrPreparationMismatch
		}
		seen[loaded.position-1] = true
		loadedRows = append(loadedRows, loaded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate card preparation proposals: %w", err)
	}
	rows.Close()
	for _, exists := range seen {
		if !exists {
			return nil, transfer_preparation.ErrProposalNotFound
		}
	}

	storedProposals := make([]transfer_preparation.StoredProposal, len(queries))
	var decodeGroup errgroup.Group
	decodeGroup.SetLimit(proposalDecodeConcurrency)
	for _, loaded := range loadedRows {
		loaded := loaded
		decodeGroup.Go(func() error {
			index := int(loaded.position - 1)
			query := queries[index]
			stored := loaded.stored
			stored.TransferID = query.TransferID
			stored.GroupTargetID = query.GroupTargetID
			if err := decodeStoredProposal(
				&stored,
				query,
				loaded.requestPayload,
				loaded.snapshotPayload,
				loaded.transferItemIDs,
				loaded.itemPositions,
				loaded.vendorCodes,
			); err != nil {
				return err
			}
			storedProposals[index] = stored
			return nil
		})
	}
	if err := decodeGroup.Wait(); err != nil {
		return nil, err
	}
	return storedProposals, nil
}

func decodeStoredProposal(
	stored *transfer_preparation.StoredProposal,
	query transfer_preparation.ProposalQuery,
	requestPayload []byte,
	snapshotPayload []byte,
	transferItemIDs []int64,
	itemPositions []int64,
	vendorCodes []string,
) error {
	if stored == nil || len(transferItemIDs) == 0 ||
		len(transferItemIDs) != len(itemPositions) ||
		len(transferItemIDs) != len(vendorCodes) {
		return transfer_preparation.ErrPreparationMismatch
	}
	if err := json.Unmarshal(requestPayload, &stored.Proposal.Request); err != nil {
		return fmt.Errorf("decode card preparation request: %w", err)
	}
	stored.Proposal.EncodedRequest = append([]byte(nil), requestPayload...)
	if err := json.Unmarshal(snapshotPayload, &stored.Proposal.MetadataSnapshot); err != nil {
		return fmt.Errorf("decode card preparation metadata snapshot: %w", err)
	}
	stored.Proposal.LimitsObservedAt = stored.Proposal.LimitsObservedAt.UTC()
	stored.Members = make([]transfer_preparation.PreparedMember, len(transferItemIDs))
	for index := range transferItemIDs {
		if transferItemIDs[index] <= 0 || itemPositions[index] <= 0 ||
			(index > 0 && itemPositions[index] <= itemPositions[index-1]) ||
			strings.TrimSpace(vendorCodes[index]) != vendorCodes[index] ||
			vendorCodes[index] == "" {
			return transfer_preparation.ErrPreparationMismatch
		}
		stored.Members[index] = transfer_preparation.PreparedMember{
			TransferItemID: transferItemIDs[index],
			ItemPosition:   int(itemPositions[index]),
			VendorCode:     vendorCodes[index],
		}
	}
	if err := validateStoredProposal(*stored); err != nil {
		return err
	}
	if len(stored.Members) != len(stored.Proposal.Request[0].Variants) {
		return transfer_preparation.ErrPreparationMismatch
	}
	for position, member := range stored.Members {
		if member.VendorCode != stored.Proposal.Request[0].Variants[position].VendorCode {
			return transfer_preparation.ErrPreparationMismatch
		}
	}
	return nil
}

func validateStoredProposal(stored transfer_preparation.StoredProposal) error {
	if stored.SourceGroupID <= 0 ||
		stored.TargetID <= 0 ||
		strings.TrimSpace(string(stored.SellerID)) != string(stored.SellerID) ||
		stored.SellerID == "" || stored.Proposal.Validate() != nil {
		return transfer_preparation.ErrPreparationMismatch
	}
	return nil
}
