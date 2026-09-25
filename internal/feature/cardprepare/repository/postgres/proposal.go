package cardprepare_postgres_repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	"golang.org/x/sync/errgroup"
)

const (
	maxProposalBatchSize      = 100
	proposalDecodeConcurrency = 8
)

type storedProposalRow struct {
	position                       int64
	preparationID                  int64
	stored                         cardprepare_service.StoredProposal
	semanticDigest, metadataDigest []byte
	proposalRoot, requestPayload   []byte
	snapshotPayload                []byte
	transferItemIDs, itemPositions []int64
	vendorCodes                    []string
}

func (repository *Repository) LoadProposal(
	ctx context.Context,
	query cardprepare_service.ProposalQuery,
) (cardprepare_service.StoredProposal, error) {
	stored, err := repository.LoadProposals(ctx, []cardprepare_service.ProposalQuery{query})
	if err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	return stored[0], nil
}

func (repository *Repository) LoadProposals(
	ctx context.Context,
	queries []cardprepare_service.ProposalQuery,
) ([]cardprepare_service.StoredProposal, error) {
	if ctx == nil {
		return nil, errors.New("load card preparation proposals: context is nil")
	}
	if len(queries) == 0 || len(queries) > maxProposalBatchSize {
		return nil, errors.New("card preparation proposal batch size is invalid")
	}
	transferID := queries[0].TransferID
	preparationGroupIDs := make([]int64, len(queries))
	groupTargetIDs := make([]int64, len(queries))
	proposalRoots := make([][]byte, len(queries))
	for index, query := range queries {
		if err := query.Validate(); err != nil {
			return nil, err
		}
		if query.TransferID != transferID {
			return nil, errors.New("card preparation proposal batch spans transfers")
		}
		preparationGroupIDs[index] = int64(query.PreparationGroupID)
		groupTargetIDs[index] = query.GroupTargetID
		proposalRoots[index] = append([]byte(nil), query.ProposalRoot[:]...)
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	const load = `
		WITH requested AS (
			SELECT
				request.preparation_group_id,
				request.group_target_id,
				request.proposal_root,
				request.position
			FROM UNNEST($2::bigint[], $3::bigint[], $4::bytea[])
				WITH ORDINALITY AS request(
					preparation_group_id,
					group_target_id,
					proposal_root,
					position
				)
		)
		SELECT
			request.position,
			work.preparation_id,
			work.source_group_id,
			work.target_id,
			work.cabinet_id,
			artifact.subject_id,
			artifact.semantic_digest,
			artifact.metadata_digest,
			artifact.proposal_root,
			artifact.request_payload,
			artifact.limits_free,
			artifact.limits_paid,
			artifact.limits_observed_at,
			artifact.metadata_snapshot,
			members.transfer_item_ids,
			members.item_positions,
			members.vendor_codes
		FROM requested AS request
		JOIN wb.card_preparation_groups AS work
		  ON work.id = request.preparation_group_id
		 AND work.transfer_id = $1
		 AND work.group_target_id = request.group_target_id
		 AND work.status = 'prepared'
		 AND work.proposal_root = request.proposal_root
		JOIN wb.card_preparation_artifacts AS artifact
		  ON artifact.transfer_id = work.transfer_id
		 AND artifact.preparation_group_id = work.id
		 AND artifact.proposal_root = request.proposal_root
		JOIN LATERAL (
			SELECT
				ARRAY_AGG(item.transfer_item_id ORDER BY item.item_position) AS transfer_item_ids,
				ARRAY_AGG(item.item_position::bigint ORDER BY item.item_position) AS item_positions,
				ARRAY_AGG(item.vendor_code ORDER BY item.item_position) AS vendor_codes
			FROM wb.card_preparation_items AS item
			WHERE item.transfer_id = work.transfer_id
			  AND item.preparation_group_id = work.id
			  AND item.source_group_id = work.source_group_id
			  AND item.group_target_id = work.group_target_id
		) AS members ON TRUE
		ORDER BY request.position;
	`
	rows, err := repository.pool.Query(
		ctx,
		load,
		transferID,
		preparationGroupIDs,
		groupTargetIDs,
		proposalRoots,
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
			&loaded.preparationID,
			&loaded.stored.SourceGroupID,
			&loaded.stored.TargetID,
			&loaded.stored.CabinetID,
			&loaded.stored.Proposal.SubjectID,
			&loaded.semanticDigest,
			&loaded.metadataDigest,
			&loaded.proposalRoot,
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
			return nil, cardprepare_service.ErrPreparationMismatch
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
			return nil, cardprepare_service.ErrProposalNotFound
		}
	}

	storedProposals := make([]cardprepare_service.StoredProposal, len(queries))
	var decodeGroup errgroup.Group
	decodeGroup.SetLimit(proposalDecodeConcurrency)
	for _, loaded := range loadedRows {
		loaded := loaded
		decodeGroup.Go(func() error {
			index := int(loaded.position - 1)
			query := queries[index]
			stored := loaded.stored
			stored.PreparationID = cardprepare_service.PreparationID(loaded.preparationID)
			stored.PreparationGroupID = query.PreparationGroupID
			stored.TransferID = query.TransferID
			stored.GroupTargetID = query.GroupTargetID
			if err := decodeStoredProposal(
				&stored,
				query,
				loaded.semanticDigest,
				loaded.metadataDigest,
				loaded.proposalRoot,
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
	stored *cardprepare_service.StoredProposal,
	query cardprepare_service.ProposalQuery,
	semanticDigest []byte,
	metadataDigest []byte,
	proposalRoot []byte,
	requestPayload []byte,
	snapshotPayload []byte,
	transferItemIDs []int64,
	itemPositions []int64,
	vendorCodes []string,
) error {
	if stored == nil || len(transferItemIDs) == 0 ||
		len(transferItemIDs) != len(itemPositions) ||
		len(transferItemIDs) != len(vendorCodes) {
		return cardprepare_service.ErrPreparationMismatch
	}
	if err := decodeDigest(semanticDigest, &stored.Proposal.SemanticDigest); err != nil {
		return err
	}
	if err := decodeDigest(metadataDigest, &stored.Proposal.MetadataDigest); err != nil {
		return err
	}
	if err := decodeDigest(proposalRoot, &stored.Proposal.ProposalRoot); err != nil {
		return err
	}
	if stored.Proposal.ProposalRoot != query.ProposalRoot {
		return cardprepare_service.ErrPreparationMismatch
	}
	if err := json.Unmarshal(requestPayload, &stored.Proposal.Request); err != nil {
		return fmt.Errorf("decode card preparation request: %w", err)
	}
	canonicalRequest, err := json.Marshal(stored.Proposal.Request)
	if err != nil || !bytes.Equal(canonicalRequest, requestPayload) {
		return cardprepare_service.ErrPreparationMismatch
	}
	stored.Proposal.EncodedRequest = append([]byte(nil), requestPayload...)
	if err := json.Unmarshal(snapshotPayload, &stored.Proposal.MetadataSnapshot); err != nil {
		return fmt.Errorf("decode card preparation metadata snapshot: %w", err)
	}
	stored.Proposal.LimitsObservedAt = stored.Proposal.LimitsObservedAt.UTC()
	stored.Members = make([]cardprepare_service.PreparedMember, len(transferItemIDs))
	for index := range transferItemIDs {
		if transferItemIDs[index] <= 0 || itemPositions[index] <= 0 ||
			(index > 0 && itemPositions[index] <= itemPositions[index-1]) ||
			strings.TrimSpace(vendorCodes[index]) != vendorCodes[index] ||
			vendorCodes[index] == "" {
			return cardprepare_service.ErrPreparationMismatch
		}
		stored.Members[index] = cardprepare_service.PreparedMember{
			TransferItemID: transferItemIDs[index],
			ItemPosition:   int(itemPositions[index]),
			VendorCode:     vendorCodes[index],
		}
	}
	if err := validateStoredProposal(*stored); err != nil {
		return err
	}
	if len(stored.Members) != len(stored.Proposal.Request[0].Variants) {
		return cardprepare_service.ErrPreparationMismatch
	}
	for position, member := range stored.Members {
		if member.VendorCode != stored.Proposal.Request[0].Variants[position].VendorCode {
			return cardprepare_service.ErrPreparationMismatch
		}
	}
	return nil
}

func validateStoredProposal(stored cardprepare_service.StoredProposal) error {
	if stored.PreparationID <= 0 || stored.SourceGroupID <= 0 ||
		stored.TargetID <= 0 ||
		strings.TrimSpace(string(stored.CabinetID)) != string(stored.CabinetID) ||
		stored.CabinetID == "" || stored.Proposal.ValidateIntegrity() != nil {
		return cardprepare_service.ErrPreparationMismatch
	}
	return nil
}

func decodeDigest(source []byte, destination *cardprepare_service.Digest) error {
	if len(source) != len(destination) {
		return cardprepare_service.ErrPreparationMismatch
	}
	copy(destination[:], source)
	return nil
}

var _ cardprepare_service.ProposalReader = (*Repository)(nil)
