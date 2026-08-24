package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) HasPlan(
	ctx context.Context,
	transferID transfer_service.TransferID,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM wb.publication_plans
			WHERE transfer_id = $1
				AND state <> 'superseded'
		);
	`
	var exists bool
	if err := repository.pool.QueryRow(ctx, query, transferID).Scan(&exists); err != nil {
		return false, fmt.Errorf("query publication plan existence: %w", err)
	}
	return exists, nil
}

func (repository *Repository) InsertPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_service.PlanDraft,
) (cardpublication_service.PersistedPlan, error) {
	if tx == nil {
		return cardpublication_service.PersistedPlan{}, errors.New(
			"insert publication plan: DBTX is nil",
		)
	}
	if err := draft.Validate(); err != nil {
		return cardpublication_service.PersistedPlan{}, err
	}
	for _, observation := range draft.Observations {
		const insertObservation = `
			INSERT INTO wb.publication_observations (
				transfer_id,
				target_id,
				cabinet_id,
				kind,
				observation_digest,
				normal_count,
				trash_count,
				snapshot,
				observed_at
			)
			VALUES ($1, $2, $3, 'normal_trash_preflight', $4, $5, $6, $7, $8);
		`
		if _, err := tx.Exec(
			ctx,
			insertObservation,
			draft.TransferID,
			observation.TargetID,
			observation.CabinetID,
			observation.Digest[:],
			observation.NormalCount,
			observation.TrashCount,
			observation.Payload,
			observation.ObservedAt,
		); err != nil {
			return cardpublication_service.PersistedPlan{}, fmt.Errorf(
				"insert publication observation: %w",
				err,
			)
		}
	}

	state := "awaiting_authorization"
	var outcomeClass, outcomeCode any
	var finished bool
	if len(draft.Actions) == 0 {
		state = "terminal"
		class, code := zeroActionPlanOutcome(draft.Groups)
		outcomeClass = class
		outcomeCode = code
		finished = true
	}
	const insertPlan = `
		INSERT INTO wb.publication_plans (
			transfer_id,
			plan_digest,
			target_set_root,
			state,
			outcome_class,
			outcome_code,
			finished_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			CASE WHEN $7 THEN CURRENT_TIMESTAMP ELSE NULL END
		)
		RETURNING id;
	`
	persisted := cardpublication_service.PersistedPlan{}
	if err := tx.QueryRow(
		ctx,
		insertPlan,
		draft.TransferID,
		draft.Digest[:],
		draft.TargetSetRoot[:],
		state,
		outcomeClass,
		outcomeCode,
		finished,
	).Scan(&persisted.ID); err != nil {
		return cardpublication_service.PersistedPlan{}, fmt.Errorf(
			"insert publication plan: %w",
			err,
		)
	}

	persisted.Actions = make(
		[]cardpublication_service.PersistedAction,
		0,
		len(draft.Actions),
	)
	for _, action := range draft.Actions {
		var mediaRoot any
		if action.MediaLinkSetRoot != (cardpublication_service.Digest{}) {
			mediaRoot = action.MediaLinkSetRoot[:]
		}
		const insertAction = `
			INSERT INTO wb.publication_actions (
				transfer_id,
				plan_id,
				target_id,
				action_key,
				kind,
				request_digest,
				request_payload,
				member_set_digest,
				media_link_set_root
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id;
		`
		var actionID int64
		if err := tx.QueryRow(
			ctx,
			insertAction,
			draft.TransferID,
			persisted.ID,
			action.TargetID,
			action.Key[:],
			action.Kind,
			action.RequestDigest[:],
			action.RequestPayload,
			action.MemberSetDigest[:],
			mediaRoot,
		).Scan(&actionID); err != nil {
			return cardpublication_service.PersistedPlan{}, fmt.Errorf(
				"insert publication action: %w",
				err,
			)
		}
		for _, member := range action.Members {
			const insertMember = `
				INSERT INTO wb.publication_action_members (
					transfer_id,
					action_id,
					target_id,
					group_target_id,
					transfer_item_target_id,
					request_member_index,
					vendor_code
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7);
			`
			if _, err := tx.Exec(
				ctx,
				insertMember,
				draft.TransferID,
				actionID,
				action.TargetID,
				member.GroupTargetID,
				member.TransferItemTargetID,
				member.RequestMemberIndex,
				member.VendorCode,
			); err != nil {
				return cardpublication_service.PersistedPlan{}, fmt.Errorf(
					"insert publication action member: %w",
					err,
				)
			}
		}
		persisted.Actions = append(persisted.Actions, cardpublication_service.PersistedAction{
			ID:  actionID,
			Key: action.Key,
		})
	}

	for _, identity := range draft.Identities {
		var nmID, imtID, subjectID any
		if identity.NMID > 0 {
			nmID = identity.NMID
		}
		if identity.IMTID > 0 {
			imtID = identity.IMTID
		}
		if identity.SubjectID > 0 {
			subjectID = identity.SubjectID
		}
		const upsertIdentity = `
			INSERT INTO wb.product_identities (
				cabinet_id,
				vendor_code_key,
				normalization_version,
				state,
				nm_id,
				imt_id,
				subject_id,
				observation_digest
			)
			VALUES ($1, $2, 1, $3, $4, $5, $6, $7)
			ON CONFLICT (cabinet_id, vendor_code_key) DO UPDATE
			SET state = EXCLUDED.state,
			    nm_id = EXCLUDED.nm_id,
			    imt_id = EXCLUDED.imt_id,
			    subject_id = EXCLUDED.subject_id,
			    observation_digest = EXCLUDED.observation_digest,
			    revision = wb.product_identities.revision + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE wb.product_identities.state NOT IN (
				'mutation_pending', 'blocked_uncertain'
			);
		`
		if _, err := tx.Exec(
			ctx,
			upsertIdentity,
			identity.CabinetID,
			identity.VendorCode,
			identity.State,
			nmID,
			imtID,
			subjectID,
			identity.ObservationDigest[:],
		); err != nil {
			return cardpublication_service.PersistedPlan{}, fmt.Errorf(
				"upsert product identity: %w",
				err,
			)
		}
	}
	return persisted, nil
}

func zeroActionPlanOutcome(
	groups []cardpublication_service.PlannedGroupDraft,
) (string, string) {
	if len(groups) == 0 {
		return "skipped", "NO_PREPARED_GROUPS"
	}
	rejected, skipped := 0, 0
	for _, group := range groups {
		switch group.OutcomeClass {
		case transfer_service.ResultRejected:
			rejected++
		case transfer_service.ResultSkipped:
			skipped++
		}
	}
	switch {
	case rejected == len(groups):
		return "rejected", "PREFLIGHT_REJECTED"
	case rejected > 0 && skipped > 0:
		return "partial", "PREFLIGHT_PARTIAL"
	default:
		return "skipped", "NO_MUTATION_REQUIRED"
	}
}

var _ cardpublication_service.Repository = (*Repository)(nil)
