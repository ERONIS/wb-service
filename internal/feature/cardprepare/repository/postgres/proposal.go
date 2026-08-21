package cardprepare_postgres_repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) LoadProposal(
	ctx context.Context,
	query cardprepare_service.ProposalQuery,
) (cardprepare_service.StoredProposal, error) {
	if ctx == nil {
		return cardprepare_service.StoredProposal{}, errors.New(
			"load card preparation proposal: context is nil",
		)
	}
	if err := query.Validate(); err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()

	const load = `
		SELECT
			work.preparation_id,
			work.source_group_id,
			work.target_id,
			work.cabinet_id,
			payload.subject_id,
			payload.semantic_digest,
			payload.metadata_digest,
			payload.proposal_root,
			payload.request_payload,
			payload.limits_free,
			payload.limits_paid,
			payload.limits_observed_at,
			snapshot.metadata_digest,
			snapshot.snapshot
		FROM wb.card_preparation_groups AS work
		JOIN wb.card_preparation_payloads AS payload
			ON payload.transfer_id = work.transfer_id
		   AND payload.preparation_group_id = work.id
		JOIN wb.card_metadata_snapshots AS snapshot
			ON snapshot.transfer_id = payload.transfer_id
		   AND snapshot.preparation_group_id = payload.preparation_group_id
		   AND snapshot.id = payload.metadata_snapshot_id
		WHERE work.id = $1
			AND work.transfer_id = $2
			AND work.group_target_id = $3
			AND work.status = 'prepared'
			AND work.proposal_root = $4
			AND payload.proposal_root = $4;
	`
	stored := cardprepare_service.StoredProposal{
		PreparationGroupID: query.PreparationGroupID,
		TransferID:         query.TransferID,
		GroupTargetID:      query.GroupTargetID,
	}
	var (
		preparationID   int64
		semanticDigest  []byte
		metadataDigest  []byte
		proposalRoot    []byte
		requestPayload  []byte
		snapshotDigest  []byte
		snapshotPayload []byte
	)
	err := repository.pool.QueryRow(
		ctx,
		load,
		query.PreparationGroupID,
		query.TransferID,
		query.GroupTargetID,
		query.ProposalRoot[:],
	).Scan(
		&preparationID,
		&stored.SourceGroupID,
		&stored.TargetID,
		&stored.CabinetID,
		&stored.Proposal.SubjectID,
		&semanticDigest,
		&metadataDigest,
		&proposalRoot,
		&requestPayload,
		&stored.Proposal.Limits.FreeLimits,
		&stored.Proposal.Limits.PaidLimits,
		&stored.Proposal.LimitsObservedAt,
		&snapshotDigest,
		&snapshotPayload,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardprepare_service.StoredProposal{}, cardprepare_service.ErrProposalNotFound
	}
	if err != nil {
		return cardprepare_service.StoredProposal{}, fmt.Errorf(
			"load card preparation proposal: %w",
			err,
		)
	}
	stored.PreparationID = cardprepare_service.PreparationID(preparationID)

	if err := decodeDigest(semanticDigest, &stored.Proposal.SemanticDigest); err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	if err := decodeDigest(metadataDigest, &stored.Proposal.MetadataDigest); err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	if err := decodeDigest(proposalRoot, &stored.Proposal.ProposalRoot); err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	if !bytes.Equal(metadataDigest, snapshotDigest) ||
		stored.Proposal.ProposalRoot != query.ProposalRoot {
		return cardprepare_service.StoredProposal{}, cardprepare_service.ErrPreparationMismatch
	}
	if err := json.Unmarshal(requestPayload, &stored.Proposal.Request); err != nil {
		return cardprepare_service.StoredProposal{}, fmt.Errorf(
			"decode card preparation request: %w",
			err,
		)
	}
	canonicalRequest, err := json.Marshal(stored.Proposal.Request)
	if err != nil || !bytes.Equal(canonicalRequest, requestPayload) {
		return cardprepare_service.StoredProposal{}, cardprepare_service.ErrPreparationMismatch
	}
	stored.Proposal.EncodedRequest = append([]byte(nil), requestPayload...)
	if err := json.Unmarshal(snapshotPayload, &stored.Proposal.MetadataSnapshot); err != nil {
		return cardprepare_service.StoredProposal{}, fmt.Errorf(
			"decode card preparation metadata snapshot: %w",
			err,
		)
	}
	stored.Proposal.LimitsObservedAt = stored.Proposal.LimitsObservedAt.UTC()
	if err := validateStoredProposal(stored); err != nil {
		return cardprepare_service.StoredProposal{}, err
	}

	members, err := repository.loadProposalMembers(ctx, query, stored.SourceGroupID)
	if err != nil {
		return cardprepare_service.StoredProposal{}, err
	}
	stored.Members = members
	if len(stored.Members) != len(stored.Proposal.Request[0].Variants) {
		return cardprepare_service.StoredProposal{}, cardprepare_service.ErrPreparationMismatch
	}
	for position, member := range stored.Members {
		if member.VendorCode != stored.Proposal.Request[0].Variants[position].VendorCode {
			return cardprepare_service.StoredProposal{}, cardprepare_service.ErrPreparationMismatch
		}
	}
	return stored, nil
}

func (repository *Repository) loadProposalMembers(
	ctx context.Context,
	query cardprepare_service.ProposalQuery,
	sourceGroupID int64,
) ([]cardprepare_service.PreparedMember, error) {
	const load = `
		SELECT transfer_item_id, item_position, vendor_code
		FROM wb.card_preparation_items
		WHERE transfer_id = $1
			AND preparation_group_id = $2
			AND source_group_id = $3
			AND group_target_id = $4
		ORDER BY item_position;
	`
	rows, err := repository.pool.Query(
		ctx,
		load,
		query.TransferID,
		query.PreparationGroupID,
		sourceGroupID,
		query.GroupTargetID,
	)
	if err != nil {
		return nil, fmt.Errorf("query card preparation members: %w", err)
	}
	defer rows.Close()

	members := make([]cardprepare_service.PreparedMember, 0)
	for rows.Next() {
		var member cardprepare_service.PreparedMember
		if err := rows.Scan(
			&member.TransferItemID,
			&member.ItemPosition,
			&member.VendorCode,
		); err != nil {
			return nil, fmt.Errorf("scan card preparation member: %w", err)
		}
		if member.TransferItemID <= 0 || member.ItemPosition <= 0 ||
			strings.TrimSpace(member.VendorCode) != member.VendorCode ||
			member.VendorCode == "" {
			return nil, cardprepare_service.ErrPreparationMismatch
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate card preparation members: %w", err)
	}
	return members, nil
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
