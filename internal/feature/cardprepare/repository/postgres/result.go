package cardprepare_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

const preparationWorkColumns = `
	work.id,
	work.preparation_id,
	work.transfer_id,
	work.group_target_id,
	work.source_group_id,
	work.target_id,
	work.cabinet_id,
	work.status,
	work.revision,
	COALESCE(work.outcome_code, ''),
	COALESCE(work.outcome_item_position, 0),
	COALESCE(work.outcome_field, ''),
	work.proposal_root
`

type rowScanner interface {
	Scan(dest ...any) error
}

func (repository *Repository) ListPreparationWork(
	ctx context.Context,
	transferID transfer_service.TransferID,
) ([]cardprepare_service.PreparationWork, error) {
	if transferID <= 0 {
		return nil, cardprepare_service.ErrPreparationMismatch
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()

	query := `
		SELECT ` + preparationWorkColumns + `
		FROM wb.card_preparation_groups AS work
		WHERE work.transfer_id = $1
		ORDER BY work.id;
	`
	rows, err := repository.pool.Query(ctx, query, transferID)
	if err != nil {
		return nil, fmt.Errorf("list card preparation work: %w", err)
	}
	defer rows.Close()
	works := make([]cardprepare_service.PreparationWork, 0)
	for rows.Next() {
		work, err := scanPreparationWork(rows)
		if err != nil {
			return nil, fmt.Errorf("scan card preparation work: %w", err)
		}
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate card preparation work: %w", err)
	}
	return works, nil
}

func (repository *Repository) SavePreparationResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardprepare_service.SavePreparationResultCommand,
) (cardprepare_service.PreparationWork, error) {
	if tx == nil {
		return cardprepare_service.PreparationWork{}, errors.New(
			"save card preparation result: DBTX is nil",
		)
	}
	if err := command.Validate(); err != nil {
		return cardprepare_service.PreparationWork{}, err
	}
	work, err := lockPreparationWork(ctx, tx, command.Work.TransferID, command.Work.ID)
	if err != nil {
		return cardprepare_service.PreparationWork{}, err
	}
	if !sameWorkIdentity(work, command.Work) {
		return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
	}
	if work.Status.IsTerminal() {
		if sameStoredResult(work, command.Result) {
			return work, nil
		}
		return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
	}
	if work.Status != cardprepare_service.WorkStatusPending || work.Revision != 0 {
		return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
	}

	status := cardprepare_service.WorkStatusRejected
	if command.Result.Outcome.Code == cardprepare_service.OutcomePrepared {
		status = cardprepare_service.WorkStatusPrepared
		if err := insertProposalArtifacts(ctx, tx, command); err != nil {
			return cardprepare_service.PreparationWork{}, err
		}
	} else if command.Result.Outcome.Code == cardprepare_service.OutcomeCatalogContractError {
		status = cardprepare_service.WorkStatusUnresolved
	}

	var outcomeItem any
	if command.Result.Outcome.ItemPosition > 0 {
		outcomeItem = command.Result.Outcome.ItemPosition
	}
	var outcomeField any
	if command.Result.Outcome.Field != "" {
		outcomeField = command.Result.Outcome.Field
	}
	var proposalRoot any
	if status == cardprepare_service.WorkStatusPrepared {
		proposalRoot = command.Result.Proposal.ProposalRoot[:]
	}
	const updateWork = `
		UPDATE wb.card_preparation_groups
		SET status = $3,
		    revision = 1,
		    outcome_code = $4,
		    outcome_item_position = $5,
		    outcome_field = $6,
		    proposal_root = $7,
		    finished_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND id = $2
			AND status = 'pending'
			AND revision = 0;
	`
	result, err := tx.Exec(
		ctx,
		updateWork,
		work.TransferID,
		work.ID,
		status,
		command.Result.Outcome.Code,
		outcomeItem,
		outcomeField,
		proposalRoot,
	)
	if err != nil {
		return cardprepare_service.PreparationWork{}, fmt.Errorf(
			"update card preparation work result: %w",
			err,
		)
	}
	if result.RowsAffected() != 1 {
		return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
	}
	if err := advancePreparationProjection(
		ctx,
		tx,
		work.TransferID,
		work.PreparationID,
		status,
	); err != nil {
		return cardprepare_service.PreparationWork{}, err
	}
	return lockPreparationWork(ctx, tx, work.TransferID, work.ID)
}

func lockPreparationWork(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	workID cardprepare_service.PreparationGroupID,
) (cardprepare_service.PreparationWork, error) {
	query := `
		SELECT ` + preparationWorkColumns + `
		FROM wb.card_preparation_groups AS work
		WHERE work.transfer_id = $1 AND work.id = $2
		FOR UPDATE;
	`
	work, err := scanPreparationWork(tx.QueryRow(ctx, query, transferID, workID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
	}
	if err != nil {
		return cardprepare_service.PreparationWork{}, fmt.Errorf(
			"lock card preparation work: %w",
			err,
		)
	}
	return work, nil
}

func scanPreparationWork(row rowScanner) (cardprepare_service.PreparationWork, error) {
	var (
		work          cardprepare_service.PreparationWork
		workID        int64
		preparationID int64
		transferID    int64
		proposalRoot  []byte
	)
	if err := row.Scan(
		&workID,
		&preparationID,
		&transferID,
		&work.GroupTargetID,
		&work.SourceGroupID,
		&work.TargetID,
		&work.CabinetID,
		&work.Status,
		&work.Revision,
		&work.OutcomeCode,
		&work.OutcomeItem,
		&work.OutcomeField,
		&proposalRoot,
	); err != nil {
		return cardprepare_service.PreparationWork{}, err
	}
	work.ID = cardprepare_service.PreparationGroupID(workID)
	work.PreparationID = cardprepare_service.PreparationID(preparationID)
	work.TransferID = transfer_service.TransferID(transferID)
	if proposalRoot != nil {
		if len(proposalRoot) != len(work.ProposalRoot) {
			return cardprepare_service.PreparationWork{}, cardprepare_service.ErrPreparationMismatch
		}
		copy(work.ProposalRoot[:], proposalRoot)
	}
	if err := work.Validate(); err != nil {
		return cardprepare_service.PreparationWork{}, err
	}
	return work, nil
}

func sameWorkIdentity(
	left cardprepare_service.PreparationWork,
	right cardprepare_service.PreparationWork,
) bool {
	return left.ID == right.ID &&
		left.PreparationID == right.PreparationID &&
		left.TransferID == right.TransferID &&
		left.GroupTargetID == right.GroupTargetID &&
		left.SourceGroupID == right.SourceGroupID &&
		left.TargetID == right.TargetID &&
		left.CabinetID == right.CabinetID
}

func sameStoredResult(
	work cardprepare_service.PreparationWork,
	result cardprepare_service.Result,
) bool {
	if work.OutcomeCode != result.Outcome.Code ||
		work.OutcomeItem != result.Outcome.ItemPosition ||
		work.OutcomeField != result.Outcome.Field {
		return false
	}
	if work.Status == cardprepare_service.WorkStatusPrepared {
		return result.Proposal != nil &&
			work.ProposalRoot == result.Proposal.ProposalRoot
	}
	return result.Proposal == nil && work.ProposalRoot == (cardprepare_service.Digest{})
}

func insertProposalArtifacts(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardprepare_service.SavePreparationResultCommand,
) error {
	proposal := command.Result.Proposal
	snapshot, err := json.Marshal(proposal.MetadataSnapshot)
	if err != nil {
		return fmt.Errorf("encode card preparation metadata snapshot: %w", err)
	}
	const insertArtifact = `
		INSERT INTO wb.card_preparation_artifacts (
			preparation_group_id,
			transfer_id,
			subject_id,
			semantic_digest,
			metadata_digest,
			proposal_root,
			request_payload,
			metadata_snapshot,
			limits_free,
			limits_paid,
			limits_observed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11);
	`
	if _, err := tx.Exec(
		ctx,
		insertArtifact,
		command.Work.ID,
		command.Work.TransferID,
		proposal.SubjectID,
		proposal.SemanticDigest[:],
		proposal.MetadataDigest[:],
		proposal.ProposalRoot[:],
		proposal.EncodedRequest,
		string(snapshot),
		proposal.Limits.FreeLimits,
		proposal.Limits.PaidLimits,
		proposal.LimitsObservedAt,
	); err != nil {
		return fmt.Errorf("insert card preparation artifact: %w", err)
	}

	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "card_preparation_items"},
		[]string{
			"transfer_id",
			"preparation_group_id",
			"source_group_id",
			"group_target_id",
			"transfer_item_id",
			"item_position",
			"vendor_code",
		},
		pgx.CopyFromSlice(len(command.Members), func(index int) ([]any, error) {
			member := command.Members[index]
			return []any{
				command.Work.TransferID,
				command.Work.ID,
				command.Work.SourceGroupID,
				command.Work.GroupTargetID,
				member.TransferItemID,
				member.ItemPosition,
				member.VendorCode,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("copy card preparation members: %w", err)
	}
	if count != int64(len(command.Members)) {
		return cardprepare_service.ErrPreparationMismatch
	}
	return nil
}

func advancePreparationProjection(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
	preparationID cardprepare_service.PreparationID,
	status cardprepare_service.WorkStatus,
) error {
	const query = `
		UPDATE wb.card_preparations AS preparation
		SET completed_group_targets = completed_group_targets +
				CASE WHEN $3 = 'prepared' THEN 1 ELSE 0 END,
		    rejected_group_targets = rejected_group_targets +
				CASE WHEN $3 = 'rejected' THEN 1 ELSE 0 END,
		    unresolved_group_targets = unresolved_group_targets +
				CASE WHEN $3 = 'unresolved' THEN 1 ELSE 0 END,
		    status = CASE
				WHEN completed_group_targets + rejected_group_targets +
				     unresolved_group_targets + 1 = expected_group_targets
				THEN CASE
					WHEN rejected_group_targets + unresolved_group_targets +
					     CASE WHEN $3 IN ('rejected', 'unresolved') THEN 1 ELSE 0 END = 0
					THEN 'completed'
					ELSE 'completed_with_issues'
				END
				ELSE 'processing'
			END,
		    finished_at = CASE
				WHEN completed_group_targets + rejected_group_targets +
				     unresolved_group_targets + 1 = expected_group_targets
				THEN COALESCE(preparation.finished_at, CURRENT_TIMESTAMP)
				ELSE NULL
			END,
		    revision = preparation.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE preparation.transfer_id = $1
			AND preparation.id = $2
			AND preparation.status = 'processing'
			AND completed_group_targets + rejected_group_targets +
			    unresolved_group_targets < expected_group_targets;
	`
	result, err := tx.Exec(ctx, query, transferID, preparationID, status)
	if err != nil {
		return fmt.Errorf("advance card preparation projection: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardprepare_service.ErrPreparationMismatch
	}
	return nil
}

var _ cardprepare_service.PreparationRepository = (*Repository)(nil)
