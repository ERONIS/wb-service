package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_sequence "github.com/ERONIS/wb-service/internal/core/repository/postgres/sequence"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	cardpublication_planning "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/planning"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

func (repository *Repository) InsertPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_model.PlanDraft,
) (cardpublication_model.PersistedPlan, error) {
	if tx == nil {
		return cardpublication_model.PersistedPlan{}, errors.New(
			"insert publication plan: DBTX is nil",
		)
	}
	if err := draft.Validate(); err != nil {
		return cardpublication_model.PersistedPlan{}, err
	}
	if err := insertPlanObservations(ctx, tx, draft); err != nil {
		return cardpublication_model.PersistedPlan{}, err
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
			state,
			outcome_class,
			outcome_code,
			finished_at
		)
		VALUES (
			$1, $2, $3, $4,
			CASE WHEN $5 THEN CURRENT_TIMESTAMP ELSE NULL END
		)
		RETURNING id;
	`
	persisted := cardpublication_model.PersistedPlan{}
	if err := tx.QueryRow(
		ctx,
		insertPlan,
		draft.TransferID,
		state,
		outcomeClass,
		outcomeCode,
		finished,
	).Scan(&persisted.ID); err != nil {
		return cardpublication_model.PersistedPlan{}, fmt.Errorf(
			"insert publication plan: %w",
			err,
		)
	}

	actionIDs, err := reservePublicationActionIDs(ctx, tx, len(draft.Actions))
	if err != nil {
		return cardpublication_model.PersistedPlan{}, err
	}
	if err := copyPlanActions(ctx, tx, draft, persisted.ID, actionIDs); err != nil {
		return cardpublication_model.PersistedPlan{}, err
	}
	if err := copyPlanActionMembers(ctx, tx, draft, actionIDs); err != nil {
		return cardpublication_model.PersistedPlan{}, err
	}
	if err := upsertPlanIdentities(ctx, tx, draft.Identities); err != nil {
		return cardpublication_model.PersistedPlan{}, err
	}
	persisted.Actions = make([]cardpublication_model.PersistedAction, len(draft.Actions))
	for index, action := range draft.Actions {
		persisted.Actions[index] = cardpublication_model.PersistedAction{
			ID: actionIDs[index], Position: action.Position,
		}
	}
	return persisted, nil
}

func insertPlanObservations(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_model.PlanDraft,
) error {
	if len(draft.Observations) == 0 {
		return nil
	}
	targetIDs := make([]int64, len(draft.Observations))
	sellerIDs := make([]string, len(draft.Observations))
	normalCounts := make([]int32, len(draft.Observations))
	trashCounts := make([]int32, len(draft.Observations))
	snapshots := make([]string, len(draft.Observations))
	observedAt := make([]time.Time, len(draft.Observations))
	for index, observation := range draft.Observations {
		targetIDs[index] = observation.TargetID
		sellerIDs[index] = string(observation.SellerID)
		normalCounts[index] = int32(observation.NormalCount)
		trashCounts[index] = int32(observation.TrashCount)
		snapshots[index] = string(observation.Payload)
		observedAt[index] = observation.ObservedAt
	}
	const insert = `
		INSERT INTO wb.publication_observations (
			transfer_id, target_id, seller_id, kind,
			normal_count, trash_count, snapshot, observed_at
		)
		SELECT
			$1, source.target_id, source.seller_id, 'normal_trash_preflight',
			source.normal_count, source.trash_count,
			source.snapshot::jsonb, source.observed_at
		FROM UNNEST(
			$2::bigint[], $3::text[], $4::integer[],
			$5::integer[], $6::text[], $7::timestamptz[]
		) AS source(
			target_id, seller_id, normal_count,
			trash_count, snapshot, observed_at
		)
	`
	if _, err := tx.Exec(
		ctx,
		insert,
		draft.TransferID,
		targetIDs,
		sellerIDs,
		normalCounts,
		trashCounts,
		snapshots,
		observedAt,
	); err != nil {
		return fmt.Errorf("insert publication observations: %w", err)
	}
	return nil
}

func reservePublicationActionIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	count int,
) ([]int64, error) {
	if count == 0 {
		return nil, nil
	}
	ids, err := core_postgres_sequence.ReserveIDs(
		ctx, tx, "wb.publication_actions_id_seq", count,
	)
	if err != nil {
		return nil, fmt.Errorf("reserve publication action IDs: %w", err)
	}
	if len(ids) != count {
		return nil, fmt.Errorf("reserved %d of %d publication action IDs", len(ids), count)
	}
	return ids, nil
}

func copyPlanActions(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_model.PlanDraft,
	planID int64,
	actionIDs []int64,
) error {
	if len(draft.Actions) == 0 {
		return nil
	}
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "publication_actions"},
		[]string{
			"id", "transfer_id", "plan_id", "target_id", "position", "kind",
			"request_payload",
		},
		pgx.CopyFromSlice(len(draft.Actions), func(index int) ([]any, error) {
			action := draft.Actions[index]
			return []any{
				actionIDs[index], draft.TransferID, planID, action.TargetID,
				action.Position, string(action.Kind), action.RequestPayload,
			}, nil
		}),
	)
	return exactPlanCopy("publication actions", count, len(draft.Actions), err)
}

func copyPlanActionMembers(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_model.PlanDraft,
	actionIDs []int64,
) error {
	membersCount := 0
	for _, action := range draft.Actions {
		membersCount += len(action.Members)
	}
	if membersCount == 0 {
		return nil
	}
	rows := make([][]any, 0, membersCount)
	for actionIndex, action := range draft.Actions {
		for _, member := range action.Members {
			rows = append(rows, []any{
				draft.TransferID,
				actionIDs[actionIndex],
				action.TargetID,
				member.GroupTargetID,
				member.TransferItemTargetID,
				member.RequestMemberIndex,
				member.VendorCode,
			})
		}
	}
	count, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"wb", "publication_action_members"},
		[]string{
			"transfer_id", "action_id", "target_id", "group_target_id",
			"transfer_item_target_id", "request_member_index", "vendor_code",
		},
		pgx.CopyFromRows(rows),
	)
	return exactPlanCopy("publication action members", count, membersCount, err)
}

func upsertPlanIdentities(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	identities []cardpublication_model.IdentityDraft,
) error {
	if len(identities) == 0 {
		return nil
	}
	sellerIDs := make([]string, len(identities))
	vendorCodes := make([]string, len(identities))
	states := make([]string, len(identities))
	nmIDs := make([]int64, len(identities))
	imtIDs := make([]int64, len(identities))
	subjectIDs := make([]int64, len(identities))
	for index, identity := range identities {
		sellerIDs[index] = string(identity.SellerID)
		vendorCodes[index] = identity.VendorCode
		states[index] = string(identity.State)
		nmIDs[index] = identity.NMID
		imtIDs[index] = identity.IMTID
		subjectIDs[index] = identity.SubjectID
	}
	const upsert = `
		WITH incoming AS (
			SELECT *
			FROM UNNEST(
				$1::text[], $2::text[], $3::text[], $4::bigint[],
				$5::bigint[], $6::bigint[]
			) WITH ORDINALITY AS source(
				seller_id, vendor_code_key, state, nm_id, imt_id,
				subject_id, position
			)
		), deduplicated AS (
			SELECT DISTINCT ON (seller_id, vendor_code_key)
				seller_id, vendor_code_key, state, nm_id, imt_id,
				subject_id
			FROM incoming
			ORDER BY seller_id, vendor_code_key, position DESC
		)
		INSERT INTO wb.product_identities (
			seller_id, vendor_code_key, normalization_version, state,
			nm_id, imt_id, subject_id
		)
		SELECT
			seller_id, vendor_code_key, 1, state,
			NULLIF(nm_id, 0), NULLIF(imt_id, 0), NULLIF(subject_id, 0)
		FROM deduplicated
		ON CONFLICT (seller_id, vendor_code_key) DO UPDATE
		SET state = EXCLUDED.state,
		    nm_id = EXCLUDED.nm_id,
		    imt_id = EXCLUDED.imt_id,
		    subject_id = EXCLUDED.subject_id,
		    revision = wb.product_identities.revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE wb.product_identities.state NOT IN (
			'mutation_pending', 'blocked_uncertain'
		);
	`
	if _, err := tx.Exec(
		ctx,
		upsert,
		sellerIDs,
		vendorCodes,
		states,
		nmIDs,
		imtIDs,
		subjectIDs,
	); err != nil {
		return fmt.Errorf("upsert publication plan identities: %w", err)
	}
	return nil
}

func exactPlanCopy(name string, copied int64, expected int, err error) error {
	if err != nil {
		return fmt.Errorf("copy %s: %w", name, err)
	}
	if copied != int64(expected) {
		return fmt.Errorf("copied %d of %d %s", copied, expected, name)
	}
	return nil
}

func zeroActionPlanOutcome(
	groups []cardpublication_model.PlannedGroupDraft,
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

var _ cardpublication_planning.Repository = (*Repository)(nil)
