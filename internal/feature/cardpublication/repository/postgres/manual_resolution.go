package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_resolution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/resolution"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const manualResolutionColumns = `
	resolution.id,
	resolution.transfer_id,
	resolution.action_id,
	resolution.action_member_id,
	resolution.kind,
	resolution.result_action_revision,
	resolution.result_outcome_class,
	resolution.result_outcome_code,
	resolution.nm_id,
	resolution.imt_id,
	resolution.subject_id,
	resolution.created_at
`

func (repository *Repository) LoadManualResolutionReplay(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	idempotencyKey string,
	actorID int64,
	command cardpublication_resolution.ManualResolutionCommand,
) (cardpublication_resolution.ManualResolution, bool, error) {
	if tx == nil || idempotencyKey == "" {
		return cardpublication_resolution.ManualResolution{}, false,
			errors.New("manual resolution replay query is invalid")
	}
	const query = `
		SELECT
			resolution.actor_id,
			resolution.expected_action_revision,
			COALESCE(resolution.evidence_observation_id, 0),
			COALESCE(resolution.evidence_error_batch_id, 0),
			` + manualResolutionColumns + `
		FROM wb.publication_manual_resolutions AS resolution
		WHERE resolution.idempotency_key = $1;
	`
	var storedActor, storedRevision, storedObservation, storedErrorBatch int64
	resolution, err := scanManualResolutionWithPrefix(
		tx.QueryRow(ctx, query, idempotencyKey),
		[]any{&storedActor, &storedRevision, &storedObservation, &storedErrorBatch},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_resolution.ManualResolution{}, false, nil
	}
	if err != nil {
		return cardpublication_resolution.ManualResolution{}, false,
			fmt.Errorf("load manual resolution replay: %w", err)
	}
	if storedActor != actorID || resolution.TransferID != command.TransferID ||
		resolution.ActionID != command.ActionID ||
		resolution.ActionMemberID != command.ActionMemberID ||
		resolution.Kind != command.Kind || storedRevision != command.ExpectedActionRevision ||
		storedObservation != command.EvidenceObservationID ||
		storedErrorBatch != command.EvidenceErrorBatchID {
		return cardpublication_resolution.ManualResolution{}, true,
			cardpublication_service.ErrManualResolutionConflict
	}
	return resolution, true, nil
}

func (repository *Repository) LockResolutionSubject(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_resolution.ResolutionSubjectKey,
) (cardpublication_resolution.ManualResolutionSubject, error) {
	if tx == nil {
		return cardpublication_resolution.ManualResolutionSubject{},
			errors.New("lock manual resolution subject: DBTX is nil")
	}
	var lockedTransferID int64
	if err := tx.QueryRow(ctx, `
		SELECT id FROM wb.transfers WHERE id = $1 FOR UPDATE;
	`, command.TransferID).Scan(&lockedTransferID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardpublication_resolution.ManualResolutionSubject{},
				cardpublication_service.ErrManualResolutionConflict
		}
		return cardpublication_resolution.ManualResolutionSubject{},
			fmt.Errorf("lock transfer for publication resolution: %w", err)
	}
	const query = `
		SELECT
			action.transfer_id,
			action.id,
			member.id,
			action.revision,
			action.plan_id,
			action.target_id,
			target.seller_id,
			action.kind,
			action.request_payload,
			member.group_target_id,
			member.transfer_item_target_id,
			item_target.revision,
			member.vendor_code,
			member.outcome_class,
			member.outcome_code,
			member.nm_id,
			attempt.id,
			(
				SELECT observation.id
				FROM wb.publication_observations AS observation
				WHERE observation.transfer_id = action.transfer_id
					AND observation.target_id = action.target_id
					AND observation.kind = 'normal_trash_preflight'
					AND observation.created_at <= action.created_at
				ORDER BY observation.id DESC
				LIMIT 1
			),
			attempt.started_at,
			item_target.attention_closed_at,
			identity.state,
			identity.nm_id,
			identity.imt_id,
			identity.subject_id,
			identity.active_transfer_id,
			identity.active_action_id
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfers AS transfer
		  ON transfer.id = action.transfer_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.publication_action_members AS member
		  ON member.transfer_id = action.transfer_id
		 AND member.action_id = action.id
		JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		JOIN wb.transfer_item_targets AS item_target
		  ON item_target.transfer_id = member.transfer_id
		 AND item_target.group_target_id = member.group_target_id
		 AND item_target.id = member.transfer_item_target_id
		JOIN wb.product_identities AS identity
		  ON identity.seller_id = target.seller_id
		 AND identity.vendor_code_key = member.vendor_code
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND member.id = $3
			AND action.revision = $4
			AND action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'terminal'
			AND action.outcome_class IN ('unresolved', 'internal_error')
			AND member.outcome_class IN ('unresolved', 'internal_error')
			AND member.outcome_code IS NOT NULL
			AND item_target.state = 'terminal'
			AND item_target.source_action_id = action.id
			AND item_target.outcome_class = member.outcome_class
			AND item_target.outcome_code = member.outcome_code
			AND COALESCE(item_target.nm_id, 0) = COALESCE(member.nm_id, 0)
			AND attempt.finished_at IS NOT NULL
			AND ($5::bigint = 0 OR attempt.id = $5)
			AND transfer.phase = 'finished'
			AND transfer.outcome = 'unresolved'
		FOR UPDATE OF action, plan, member, item_target, identity;
	`
	var subject cardpublication_resolution.ManualResolutionSubject
	var (
		transferID                  int64
		memberNMID                  pgtype.Int8
		preflightID                 pgtype.Int8
		attentionClosed             pgtype.Timestamptz
		identityNMID, identityIMTID pgtype.Int8
		identitySubjectID           pgtype.Int8
		identityActiveTransferID    pgtype.Int8
		identityActiveActionID      pgtype.Int8
	)
	err := tx.QueryRow(
		ctx,
		query,
		command.TransferID,
		command.ActionID,
		command.ActionMemberID,
		command.ExpectedActionRevision,
		command.AttemptID,
	).Scan(
		&transferID,
		&subject.ActionID,
		&subject.ActionMemberID,
		&subject.ActionRevision,
		&subject.PlanID,
		&subject.TargetID,
		&subject.SellerID,
		&subject.ActionKind,
		&subject.RequestPayload,
		&subject.GroupTargetID,
		&subject.TransferItemTargetID,
		&subject.ItemTargetRevision,
		&subject.VendorCode,
		&subject.MemberOutcomeClass,
		&subject.MemberOutcomeCode,
		&memberNMID,
		&subject.AttemptID,
		&preflightID,
		&subject.AttemptStartedAt,
		&attentionClosed,
		&subject.IdentityState,
		&identityNMID,
		&identityIMTID,
		&identitySubjectID,
		&identityActiveTransferID,
		&identityActiveActionID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_resolution.ManualResolutionSubject{},
			cardpublication_service.ErrManualResolutionConflict
	}
	if err != nil {
		return cardpublication_resolution.ManualResolutionSubject{},
			fmt.Errorf("lock manual resolution subject: %w", err)
	}
	subject.TransferID = transfer_model.TransferID(transferID)
	if !preflightID.Valid {
		return cardpublication_resolution.ManualResolutionSubject{},
			cardpublication_service.ErrManualResolutionConflict
	}
	subject.PreflightObservationID = preflightID.Int64
	if memberNMID.Valid {
		subject.MemberNMID = memberNMID.Int64
	}
	if attentionClosed.Valid {
		value := attentionClosed.Time.UTC()
		subject.AttentionClosedAt = &value
	}
	if identityNMID.Valid {
		subject.IdentityNMID = identityNMID.Int64
	}
	if identityIMTID.Valid {
		subject.IdentityIMTID = identityIMTID.Int64
	}
	if identitySubjectID.Valid {
		subject.IdentitySubjectID = identitySubjectID.Int64
	}
	if identityActiveTransferID.Valid {
		subject.IdentityActiveTransferID = transfer_model.TransferID(
			identityActiveTransferID.Int64,
		)
	}
	if identityActiveActionID.Valid {
		subject.IdentityActiveActionID = identityActiveActionID.Int64
	}
	if subject.TransferID != command.TransferID ||
		subject.ActionID != command.ActionID ||
		subject.ActionMemberID != command.ActionMemberID ||
		subject.ActionRevision != command.ExpectedActionRevision ||
		subject.AttemptID <= 0 ||
		subject.PreflightObservationID <= 0 || subject.AttemptStartedAt.IsZero() ||
		subject.VendorCode == "" {
		return cardpublication_resolution.ManualResolutionSubject{},
			cardpublication_service.ErrManualResolutionConflict
	}
	switch subject.IdentityState {
	case "blocked_uncertain":
		if subject.IdentityActiveTransferID != subject.TransferID ||
			subject.IdentityActiveActionID != subject.ActionID {
			return cardpublication_resolution.ManualResolutionSubject{},
				cardpublication_service.ErrManualResolutionConflict
		}
	case "remote_present":
		if subject.IdentityActiveTransferID != 0 || subject.IdentityActiveActionID != 0 {
			return cardpublication_resolution.ManualResolutionSubject{},
				cardpublication_service.ErrManualResolutionConflict
		}
	default:
		return cardpublication_resolution.ManualResolutionSubject{},
			cardpublication_service.ErrManualResolutionConflict
	}
	return subject, nil
}

func (repository *Repository) LoadManualObservationEvidence(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	subject cardpublication_resolution.ManualResolutionSubject,
	observationID int64,
) (cardpublication_resolution.ManualObservationEvidence, error) {
	if tx == nil || observationID <= 0 {
		return cardpublication_resolution.ManualObservationEvidence{},
			cardpublication_service.ErrManualEvidenceInvalid
	}
	const query = `
		SELECT
			observation.id,
			observation.snapshot,
			observation.observed_at
		FROM wb.publication_observations AS observation
		WHERE observation.transfer_id = $1
			AND observation.id = $2
			AND observation.target_id = $3
			AND observation.seller_id = $4
			AND observation.kind = 'post_submission'
			AND observation.observed_at >= $5;
	`
	var evidence cardpublication_resolution.ManualObservationEvidence
	err := tx.QueryRow(
		ctx,
		query,
		subject.TransferID,
		observationID,
		subject.TargetID,
		subject.SellerID,
		subject.AttemptStartedAt,
	).Scan(&evidence.ID, &evidence.Payload, &evidence.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_resolution.ManualObservationEvidence{},
			cardpublication_service.ErrManualEvidenceInvalid
	}
	if err != nil {
		return cardpublication_resolution.ManualObservationEvidence{},
			fmt.Errorf("load manual observation evidence: %w", err)
	}
	evidence.ObservedAt = evidence.ObservedAt.UTC()
	return evidence, nil
}

func (repository *Repository) LoadManualErrorEvidence(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	subject cardpublication_resolution.ManualResolutionSubject,
	errorBatchID int64,
) (cardpublication_resolution.ManualErrorEvidence, error) {
	if tx == nil || errorBatchID <= 0 {
		return cardpublication_resolution.ManualErrorEvidence{},
			cardpublication_service.ErrManualEvidenceInvalid
	}
	const query = `
		SELECT batch.id, batch.error_codes
		FROM wb.publication_attempts AS attempt
		JOIN wb.publication_error_batches AS batch
		  ON batch.seller_id = attempt.baseline_seller_id
		WHERE attempt.transfer_id = $1
			AND attempt.id = $2
			AND attempt.action_id = $3
			AND batch.id = $4
			AND batch.rejected_vendor_codes @> ARRAY[$5]::text[]
			AND (
				attempt.baseline_cursor_updated_at IS NULL
				OR batch.batch_updated_at > attempt.baseline_cursor_updated_at
				OR (
					batch.batch_updated_at = attempt.baseline_cursor_updated_at
					AND batch.batch_uuid > attempt.baseline_cursor_batch_uuid
				)
			);
	`
	var evidence cardpublication_resolution.ManualErrorEvidence
	err := tx.QueryRow(
		ctx,
		query,
		subject.TransferID,
		subject.AttemptID,
		subject.ActionID,
		errorBatchID,
		subject.VendorCode,
	).Scan(&evidence.ID, &evidence.ErrorCodes)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_resolution.ManualErrorEvidence{},
			cardpublication_service.ErrManualEvidenceInvalid
	}
	if err != nil {
		return cardpublication_resolution.ManualErrorEvidence{},
			fmt.Errorf("load manual Error List evidence: %w", err)
	}
	return evidence, nil
}

func (repository *Repository) ApplyManualResolution(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_resolution.ApplyManualResolutionCommand,
) (cardpublication_resolution.ManualResolution, error) {
	if tx == nil || command.Subject.TransferID != command.Command.TransferID ||
		command.Subject.ActionID != command.Command.ActionID ||
		command.Subject.ActionMemberID != command.Command.ActionMemberID ||
		command.Subject.ActionRevision != command.Command.ExpectedActionRevision ||
		command.Decision.Kind != command.Command.Kind ||
		!command.Decision.OutcomeClass.IsValid() || command.Decision.OutcomeCode == "" {
		return cardpublication_resolution.ManualResolution{},
			errors.New("apply manual resolution command is invalid")
	}
	if !command.Decision.CloseAttentionNoRetry {
		if err := applyManualMemberResult(ctx, tx, command); err != nil {
			return cardpublication_resolution.ManualResolution{}, err
		}
	}
	if err := applyManualEvidence(ctx, tx, command); err != nil {
		return cardpublication_resolution.ManualResolution{}, err
	}
	resultRevision, err := reduceManualPublicationAction(ctx, tx, command)
	if err != nil {
		return cardpublication_resolution.ManualResolution{}, err
	}
	if err := reduceManualPublicationPlan(
		ctx,
		tx,
		command.Subject.TransferID,
		command.Subject.PlanID,
	); err != nil {
		return cardpublication_resolution.ManualResolution{}, err
	}

	const insert = `
		INSERT INTO wb.publication_manual_resolutions AS resolution (
			transfer_id, action_id, action_member_id, kind,
			idempotency_key, actor_id,
			actor_name, expected_action_revision, result_action_revision,
			evidence_observation_id, evidence_error_batch_id,
			result_outcome_class, result_outcome_code,
			nm_id, imt_id, subject_id
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16
		)
		RETURNING ` + manualResolutionColumns + `;
	`
	resolution, err := scanManualResolution(tx.QueryRow(
		ctx,
		insert,
		command.Subject.TransferID,
		command.Subject.ActionID,
		command.Subject.ActionMemberID,
		command.Decision.Kind,
		command.Command.IdempotencyKey,
		command.Actor.ID,
		command.Actor.DisplayName,
		command.Subject.ActionRevision,
		resultRevision,
		nullablePositive(command.Decision.EvidenceObservationID),
		nullablePositive(command.Decision.EvidenceErrorBatchID),
		command.Decision.OutcomeClass,
		command.Decision.OutcomeCode,
		nullablePositive(command.Decision.NMID),
		nullablePositive(command.Decision.IMTID),
		nullablePositive(command.Decision.SubjectID),
	))
	if err != nil {
		return cardpublication_resolution.ManualResolution{},
			fmt.Errorf("insert publication manual resolution: %w", err)
	}
	return resolution, nil
}

func applyManualMemberResult(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_resolution.ApplyManualResolutionCommand,
) error {
	const updateMember = `
		UPDATE wb.publication_action_members
		SET outcome_class = $4,
		    outcome_code = $5,
		    nm_id = $6,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
			AND action_id = $2
			AND id = $3
			AND outcome_class = $7
			AND outcome_code = $8;
	`
	result, err := tx.Exec(
		ctx,
		updateMember,
		command.Subject.TransferID,
		command.Subject.ActionID,
		command.Subject.ActionMemberID,
		command.Decision.OutcomeClass,
		command.Decision.OutcomeCode,
		nullablePositive(command.Decision.NMID),
		command.Subject.MemberOutcomeClass,
		command.Subject.MemberOutcomeCode,
	)
	if err != nil {
		return fmt.Errorf("correct publication action member: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ErrManualResolutionConflict
	}

	switch command.Decision.Kind {
	case cardpublication_service.ManualMarkRemotePresent:
		const updateIdentity = `
			UPDATE wb.product_identities
			SET state = 'remote_present',
			    nm_id = $5,
			    imt_id = $6,
			    subject_id = $7,
			    active_transfer_id = NULL,
			    active_action_id = NULL,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE seller_id = $1
				AND vendor_code_key = $2
				AND state IN ('blocked_uncertain', 'remote_present')
				AND (
					(active_transfer_id = $3 AND active_action_id = $4)
					OR (active_transfer_id IS NULL AND active_action_id IS NULL)
				);
		`
		result, err = tx.Exec(
			ctx,
			updateIdentity,
			command.Subject.SellerID,
			command.Subject.VendorCode,
			command.Subject.TransferID,
			command.Subject.ActionID,
			command.Decision.NMID,
			command.Decision.IMTID,
			command.Decision.SubjectID,
		)
	case cardpublication_service.ManualMarkRejected:
		const updateIdentity = `
			UPDATE wb.product_identities
			SET state = CASE
			        WHEN state = 'remote_present' THEN 'remote_present'
			        ELSE 'rejected'
			    END,
			    nm_id = CASE WHEN state = 'remote_present' THEN nm_id ELSE NULL END,
			    imt_id = CASE WHEN state = 'remote_present' THEN imt_id ELSE NULL END,
			    subject_id = CASE WHEN state = 'remote_present' THEN subject_id ELSE NULL END,
			    active_transfer_id = NULL,
			    active_action_id = NULL,
			    revision = revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE seller_id = $1
				AND vendor_code_key = $2
				AND state IN ('blocked_uncertain', 'remote_present')
				AND (
					(active_transfer_id = $3 AND active_action_id = $4)
					OR (state = 'remote_present'
						AND active_transfer_id IS NULL AND active_action_id IS NULL)
				);
		`
		result, err = tx.Exec(
			ctx,
			updateIdentity,
			command.Subject.SellerID,
			command.Subject.VendorCode,
			command.Subject.TransferID,
			command.Subject.ActionID,
		)
	default:
		return cardpublication_service.ErrManualResolutionConflict
	}
	if err != nil {
		return fmt.Errorf("correct publication product identity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ErrManualResolutionConflict
	}
	return nil
}

func applyManualEvidence(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_resolution.ApplyManualResolutionCommand,
) error {
	switch command.Decision.Kind {
	case cardpublication_service.ManualMarkRemotePresent:
		const insertAttribution = `
			INSERT INTO wb.publication_attributions (
				transfer_id, group_target_id, action_id, action_member_id,
				seller_id, nm_id, attempt_id,
				preflight_observation_id, post_observation_id, level,
				safe_reason_code
			)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9,
				'observed_after_attempt', $10
			)
			ON CONFLICT (transfer_id, action_member_id) DO NOTHING;
		`
		if _, err := tx.Exec(
			ctx,
			insertAttribution,
			command.Subject.TransferID,
			command.Subject.GroupTargetID,
			command.Subject.ActionID,
			command.Subject.ActionMemberID,
			command.Subject.SellerID,
			command.Decision.NMID,
			command.Subject.AttemptID,
			command.Subject.PreflightObservationID,
			command.Decision.EvidenceObservationID,
			command.Decision.OutcomeCode,
		); err != nil {
			return fmt.Errorf("insert manual publication attribution: %w", err)
		}
		const verify = `
			SELECT 1
			FROM wb.publication_attributions
			WHERE transfer_id = $1
				AND action_id = $2
				AND action_member_id = $3
				AND attempt_id = $4
				AND nm_id = $5
				AND level = 'observed_after_attempt';
		`
		var one int
		if err := tx.QueryRow(
			ctx,
			verify,
			command.Subject.TransferID,
			command.Subject.ActionID,
			command.Subject.ActionMemberID,
			command.Subject.AttemptID,
			command.Decision.NMID,
		).Scan(&one); err != nil {
			return cardpublication_service.ErrManualEvidenceInvalid
		}
	case cardpublication_service.ManualMarkRejected:
		const insertCorrelation = `
			INSERT INTO wb.publication_error_correlations (
				transfer_id, action_id, action_member_id, attempt_id,
				error_batch_id, safe_result_code
			)
			VALUES ($1, $2, $3, $4, $5, 'MANUAL_ERROR_LIST_REJECTED')
			ON CONFLICT (transfer_id, action_member_id, error_batch_id)
			DO NOTHING;
		`
		if _, err := tx.Exec(
			ctx,
			insertCorrelation,
			command.Subject.TransferID,
			command.Subject.ActionID,
			command.Subject.ActionMemberID,
			command.Subject.AttemptID,
			command.Decision.EvidenceErrorBatchID,
		); err != nil {
			return fmt.Errorf("insert manual Error List correlation: %w", err)
		}
	case cardpublication_service.ManualCloseUnresolvedNoRetry:
		return nil
	default:
		return cardpublication_service.ErrManualResolutionConflict
	}
	return nil
}

func reduceManualPublicationAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_resolution.ApplyManualResolutionCommand,
) (int64, error) {
	const reduce = `
		WITH member_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
			FROM wb.publication_action_members
			WHERE transfer_id = $1 AND action_id = $2
		)
		UPDATE wb.publication_actions AS action
		SET outcome_class = CASE
				WHEN member_counts.unresolved > 0 OR member_counts.internal_error > 0
					THEN 'unresolved'
				WHEN member_counts.rejected = member_counts.total THEN 'rejected'
				WHEN member_counts.rejected > 0 AND member_counts.accepted > 0 THEN 'partial'
				ELSE 'success'
			END,
		    outcome_code = CASE
				WHEN member_counts.unresolved > 0 OR member_counts.internal_error > 0
					THEN $4
				WHEN member_counts.rejected = member_counts.total
					THEN $5
				WHEN member_counts.rejected > 0 AND member_counts.accepted > 0
					THEN $6
				ELSE $7
			END,
		    revision = action.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM member_counts
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.revision = $3
			AND action.state = 'terminal'
			AND member_counts.total > 0
		RETURNING action.revision;
	`
	var revision int64
	codes := resolutionActionCodes(command)
	if err := tx.QueryRow(
		ctx,
		reduce,
		command.Subject.TransferID,
		command.Subject.ActionID,
		command.Subject.ActionRevision,
		codes[0],
		codes[1],
		codes[2],
		codes[3],
	).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, cardpublication_service.ErrManualResolutionConflict
		}
		return 0, fmt.Errorf("reduce manual publication action: %w", err)
	}
	return revision, nil
}

func reduceManualPublicationPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	planID int64,
) error {
	const reduce = `
		WITH action_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state IN ('terminal', 'superseded')) AS terminal,
				COUNT(*) FILTER (WHERE outcome_class = 'rejected') AS rejected,
				COUNT(*) FILTER (WHERE outcome_class = 'partial') AS partial,
				COUNT(*) FILTER (WHERE outcome_class = 'unresolved') AS unresolved,
				COUNT(*) FILTER (WHERE outcome_class = 'internal_error') AS internal_error,
				COUNT(*) FILTER (WHERE outcome_class IN ('success', 'skipped')) AS accepted
			FROM wb.publication_actions
			WHERE transfer_id = $1 AND plan_id = $2
		)
		UPDATE wb.publication_plans AS plan
		SET outcome_class = CASE
				WHEN action_counts.unresolved > 0 OR action_counts.internal_error > 0
					THEN 'unresolved'
				WHEN action_counts.rejected = action_counts.total THEN 'rejected'
				WHEN action_counts.rejected > 0 OR action_counts.partial > 0 THEN 'partial'
				ELSE 'success'
			END,
		    outcome_code = CASE
				WHEN action_counts.unresolved > 0 OR action_counts.internal_error > 0
					THEN 'PUBLICATION_REQUIRES_ATTENTION'
				WHEN action_counts.rejected = action_counts.total THEN 'PUBLICATION_REJECTED'
				WHEN action_counts.rejected > 0 OR action_counts.partial > 0
					THEN 'PUBLICATION_PARTIAL'
				ELSE 'PUBLICATION_TERMINAL'
			END,
		    revision = plan.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		FROM action_counts
		WHERE plan.transfer_id = $1
			AND plan.id = $2
			AND plan.state = 'terminal'
			AND action_counts.total > 0
			AND action_counts.terminal = action_counts.total;
	`
	result, err := tx.Exec(ctx, reduce, transferID, planID)
	if err != nil {
		return fmt.Errorf("reduce manual publication plan: %w", err)
	}
	if result.RowsAffected() != 1 {
		return cardpublication_service.ErrManualResolutionConflict
	}
	return nil
}

func scanManualResolution(row interface{ Scan(...any) error }) (
	cardpublication_resolution.ManualResolution,
	error,
) {
	return scanManualResolutionWithPrefix(row, nil)
}

func scanManualResolutionWithPrefix(
	row interface{ Scan(...any) error },
	prefix []any,
) (cardpublication_resolution.ManualResolution, error) {
	var resolution cardpublication_resolution.ManualResolution
	var transferID int64
	var nmID, imtID, subjectID pgtype.Int8
	destinations := append(prefix,
		&resolution.ID,
		&transferID,
		&resolution.ActionID,
		&resolution.ActionMemberID,
		&resolution.Kind,
		&resolution.ResultActionRevision,
		&resolution.OutcomeClass,
		&resolution.OutcomeCode,
		&nmID,
		&imtID,
		&subjectID,
		&resolution.CreatedAt,
	)
	if err := row.Scan(destinations...); err != nil {
		return cardpublication_resolution.ManualResolution{}, err
	}
	resolution.TransferID = transfer_model.TransferID(transferID)
	if nmID.Valid {
		resolution.NMID = nmID.Int64
	}
	if imtID.Valid {
		resolution.IMTID = imtID.Int64
	}
	if subjectID.Valid {
		resolution.SubjectID = subjectID.Int64
	}
	resolution.CreatedAt = resolution.CreatedAt.UTC()
	return resolution, nil
}

var _ cardpublication_resolution.ManualResolutionRepository = (*Repository)(nil)
