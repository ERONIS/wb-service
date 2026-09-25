package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

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
	if err := insertPlanObservations(ctx, tx, draft); err != nil {
		return cardpublication_service.PersistedPlan{}, err
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

	actionIDs, err := reservePublicationActionIDs(ctx, tx, len(draft.Actions))
	if err != nil {
		return cardpublication_service.PersistedPlan{}, err
	}
	if err := copyPlanActions(ctx, tx, draft, persisted.ID, actionIDs); err != nil {
		return cardpublication_service.PersistedPlan{}, err
	}
	if err := copyPlanActionMembers(ctx, tx, draft, actionIDs); err != nil {
		return cardpublication_service.PersistedPlan{}, err
	}
	if err := upsertPlanIdentities(ctx, tx, draft.Identities); err != nil {
		return cardpublication_service.PersistedPlan{}, err
	}
	persisted.Actions = make([]cardpublication_service.PersistedAction, len(draft.Actions))
	for index, action := range draft.Actions {
		persisted.Actions[index] = cardpublication_service.PersistedAction{
			ID: actionIDs[index], Key: action.Key,
		}
	}
	return persisted, nil
}

func insertPlanObservations(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_service.PlanDraft,
) error {
	if len(draft.Observations) == 0 {
		return nil
	}
	targetIDs := make([]int64, len(draft.Observations))
	cabinetIDs := make([]string, len(draft.Observations))
	digests := make([][]byte, len(draft.Observations))
	normalCounts := make([]int32, len(draft.Observations))
	trashCounts := make([]int32, len(draft.Observations))
	snapshots := make([]string, len(draft.Observations))
	observedAt := make([]time.Time, len(draft.Observations))
	for index, observation := range draft.Observations {
		targetIDs[index] = observation.TargetID
		cabinetIDs[index] = string(observation.CabinetID)
		digests[index] = observation.Digest[:]
		normalCounts[index] = int32(observation.NormalCount)
		trashCounts[index] = int32(observation.TrashCount)
		snapshots[index] = string(observation.Payload)
		observedAt[index] = observation.ObservedAt
	}
	const insert = `
		INSERT INTO wb.publication_observations (
			transfer_id, target_id, cabinet_id, kind, observation_digest,
			normal_count, trash_count, snapshot, observed_at
		)
		SELECT
			$1, source.target_id, source.cabinet_id, 'normal_trash_preflight',
			source.observation_digest, source.normal_count, source.trash_count,
			source.snapshot::jsonb, source.observed_at
		FROM UNNEST(
			$2::bigint[], $3::text[], $4::bytea[], $5::integer[],
			$6::integer[], $7::text[], $8::timestamptz[]
		) AS source(
			target_id, cabinet_id, observation_digest, normal_count,
			trash_count, snapshot, observed_at
		)
		ON CONFLICT (
			transfer_id, target_id, kind, observation_digest
		) DO NOTHING;
	`
	if _, err := tx.Exec(
		ctx,
		insert,
		draft.TransferID,
		targetIDs,
		cabinetIDs,
		digests,
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
	const query = `
		SELECT nextval(pg_get_serial_sequence('wb.publication_actions', 'id'))
		FROM generate_series(1, $1);
	`
	rows, err := tx.Query(ctx, query, count)
	if err != nil {
		return nil, fmt.Errorf("reserve publication action IDs: %w", err)
	}
	ids := make([]int64, 0, count)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan reserved publication action ID: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate reserved publication action IDs: %w", err)
	}
	if len(ids) != count {
		return nil, fmt.Errorf("reserved %d of %d publication action IDs", len(ids), count)
	}
	return ids, nil
}

func copyPlanActions(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_service.PlanDraft,
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
			"id", "transfer_id", "plan_id", "target_id", "action_key", "kind",
			"request_digest", "request_payload", "member_set_digest", "media_link_set_root",
		},
		pgx.CopyFromSlice(len(draft.Actions), func(index int) ([]any, error) {
			action := draft.Actions[index]
			var mediaRoot any
			if action.MediaLinkSetRoot != (cardpublication_service.Digest{}) {
				mediaRoot = action.MediaLinkSetRoot[:]
			}
			return []any{
				actionIDs[index], draft.TransferID, planID, action.TargetID,
				action.Key[:], string(action.Kind), action.RequestDigest[:],
				action.RequestPayload, action.MemberSetDigest[:], mediaRoot,
			}, nil
		}),
	)
	return exactPlanCopy("publication actions", count, len(draft.Actions), err)
}

func copyPlanActionMembers(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	draft cardpublication_service.PlanDraft,
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
	identities []cardpublication_service.IdentityDraft,
) error {
	if len(identities) == 0 {
		return nil
	}
	cabinetIDs := make([]string, len(identities))
	vendorCodes := make([]string, len(identities))
	states := make([]string, len(identities))
	nmIDs := make([]int64, len(identities))
	imtIDs := make([]int64, len(identities))
	subjectIDs := make([]int64, len(identities))
	observationDigests := make([][]byte, len(identities))
	for index, identity := range identities {
		cabinetIDs[index] = string(identity.CabinetID)
		vendorCodes[index] = identity.VendorCode
		states[index] = string(identity.State)
		nmIDs[index] = identity.NMID
		imtIDs[index] = identity.IMTID
		subjectIDs[index] = identity.SubjectID
		observationDigests[index] = identity.ObservationDigest[:]
	}
	const upsert = `
		WITH incoming AS (
			SELECT *
			FROM UNNEST(
				$1::text[], $2::text[], $3::text[], $4::bigint[],
				$5::bigint[], $6::bigint[], $7::bytea[]
			) WITH ORDINALITY AS source(
				cabinet_id, vendor_code_key, state, nm_id, imt_id,
				subject_id, observation_digest, position
			)
		), deduplicated AS (
			SELECT DISTINCT ON (cabinet_id, vendor_code_key)
				cabinet_id, vendor_code_key, state, nm_id, imt_id,
				subject_id, observation_digest
			FROM incoming
			ORDER BY cabinet_id, vendor_code_key, position DESC
		)
		INSERT INTO wb.product_identities (
			cabinet_id, vendor_code_key, normalization_version, state,
			nm_id, imt_id, subject_id, observation_digest
		)
		SELECT
			cabinet_id, vendor_code_key, 1, state,
			NULLIF(nm_id, 0), NULLIF(imt_id, 0), NULLIF(subject_id, 0),
			observation_digest
		FROM deduplicated
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
		upsert,
		cabinetIDs,
		vendorCodes,
		states,
		nmIDs,
		imtIDs,
		subjectIDs,
		observationDigests,
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
