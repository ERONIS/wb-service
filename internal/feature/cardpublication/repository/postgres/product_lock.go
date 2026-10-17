package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// LockProductAction блокирует действие с товаром и его элементы для исполнения.
func (repository *Repository) LockProductAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	candidate cardpublication_service.ProductActionCandidate,
) (cardpublication_service.ProductAction, error) {
	if tx == nil || candidate.TransferID <= 0 || candidate.ActionID <= 0 ||
		candidate.AuthorizationID <= 0 {
		return cardpublication_service.ProductAction{}, errors.New(
			"lock publication product action: identity is invalid",
		)
	}
	const query = `
		SELECT
			action.plan_id,
			action.revision,
			action.kind,
			action.state,
			action.request_digest,
			action.request_payload,
			action.member_set_digest,
			target.seller_key,
			target.client_generation,
			target.credential_expires_at,
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
			attempt.id,
			attempt.recheck_observation_id,
			attempt.started_at,
			attempt.finished_at,
			attempt.delivery_state,
			attempt.http_status,
			attempt.safe_error_code,
			retry.id,
			retry.request_digest,
			retry.started_at,
			retry.finished_at,
			retry.delivery_state,
			retry.http_status,
			retry.response_disposition,
			retry.classifier_version,
			retry.safe_error_code,
			retry.unmatched_count,
			live_auth.revision
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.id = $5
		LEFT JOIN wb.publication_attempts AS attempt
		  ON attempt.action_id = action.id
		LEFT JOIN wb.publication_product_retries AS retry
		  ON retry.transfer_id = action.transfer_id
		 AND retry.action_id = action.id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.target_id = $3
			AND target.cabinet_id = $4
			AND plan.plan_digest = $6
			AND plan.target_set_root = $7
		FOR UPDATE OF action, plan;
	`
	action := cardpublication_service.ProductAction{ProductActionCandidate: candidate}
	var (
		requestDigest, memberDigest  []byte
		sellerKey, generation        []byte
		preflightID                  pgtype.Int8
		attemptID, recheckID         pgtype.Int8
		attemptStartedAt             pgtype.Timestamptz
		attemptFinishedAt            pgtype.Timestamptz
		attemptDelivery, attemptCode pgtype.Text
		attemptHTTPStatus            pgtype.Int4
		retryID                      pgtype.Int8
		retryDigest                  []byte
		retryStartedAt               pgtype.Timestamptz
		retryFinishedAt              pgtype.Timestamptz
		retryDelivery                pgtype.Text
		retryHTTPStatus              pgtype.Int4
		retryDisposition             pgtype.Text
		retryClassifierVersion       pgtype.Int4
		retrySafeCode                pgtype.Text
		retryUnmatchedCount          pgtype.Int4
		currentAuthorizationRevision int64
	)
	err := tx.QueryRow(
		ctx,
		query,
		candidate.TransferID,
		candidate.ActionID,
		candidate.TargetID,
		candidate.CabinetID,
		candidate.AuthorizationID,
		candidate.PlanDigest[:],
		candidate.TargetSetRoot[:],
	).Scan(
		&action.PlanID,
		&action.Revision,
		&action.Kind,
		&action.State,
		&requestDigest,
		&action.RequestPayload,
		&memberDigest,
		&sellerKey,
		&generation,
		&action.CredentialExpiresAt,
		&preflightID,
		&attemptID,
		&recheckID,
		&attemptStartedAt,
		&attemptFinishedAt,
		&attemptDelivery,
		&attemptHTTPStatus,
		&attemptCode,
		&retryID,
		&retryDigest,
		&retryStartedAt,
		&retryFinishedAt,
		&retryDelivery,
		&retryHTTPStatus,
		&retryDisposition,
		&retryClassifierVersion,
		&retrySafeCode,
		&retryUnmatchedCount,
		&currentAuthorizationRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_service.ProductAction{}, cardpublication_service.ErrProductActionConflict
	}
	if err != nil {
		return cardpublication_service.ProductAction{}, fmt.Errorf(
			"lock publication product action: %w",
			err,
		)
	}
	if len(requestDigest) != len(action.RequestDigest) ||
		len(memberDigest) != len(action.MemberSetDigest) ||
		len(sellerKey) != len(action.SellerKey) ||
		len(generation) != len(action.ClientGeneration) || !preflightID.Valid {
		return cardpublication_service.ProductAction{}, cardpublication_service.ErrProductActionConflict
	}
	copy(action.RequestDigest[:], requestDigest)
	copy(action.MemberSetDigest[:], memberDigest)
	copy(action.SellerKey[:], sellerKey)
	copy(action.ClientGeneration[:], generation)
	action.PreflightObservationID = preflightID.Int64
	action.AuthorizationRevision = currentAuthorizationRevision
	if attemptID.Valid {
		action.AttemptID = attemptID.Int64
	}
	if recheckID.Valid {
		action.RecheckObservationID = recheckID.Int64
	}
	if attemptStartedAt.Valid {
		action.AttemptStartedAt = attemptStartedAt.Time.UTC()
	}
	if attemptFinishedAt.Valid {
		action.AttemptFinishedAt = attemptFinishedAt.Time.UTC()
	}
	if attemptDelivery.Valid {
		action.AttemptDelivery = cardpublication_service.SubmissionDelivery(attemptDelivery.String)
	}
	if attemptHTTPStatus.Valid {
		action.AttemptHTTPStatus = int(attemptHTTPStatus.Int32)
	}
	if attemptCode.Valid {
		action.AttemptSafeCode = attemptCode.String
	}
	if retryID.Valid {
		if len(retryDigest) != len(action.EmptyResponseRetry.RequestDigest) || !retryStartedAt.Valid {
			return cardpublication_service.ProductAction{}, cardpublication_service.ErrProductActionConflict
		}
		retry := cardpublication_service.ProductRetry{
			ID:              retryID.Int64,
			TransferID:      action.TransferID,
			ActionID:        action.ActionID,
			AuthorizationID: action.AuthorizationID,
			StartedAt:       retryStartedAt.Time.UTC(),
		}
		copy(retry.RequestDigest[:], retryDigest)
		if retryFinishedAt.Valid {
			retry.FinishedAt = retryFinishedAt.Time.UTC()
		}
		if retryDelivery.Valid {
			retry.Delivery = cardpublication_service.SubmissionDelivery(retryDelivery.String)
		}
		if retryHTTPStatus.Valid {
			retry.HTTPStatus = int(retryHTTPStatus.Int32)
		}
		if retryDisposition.Valid {
			retry.Disposition = cardpublication_service.SubmissionDisposition(retryDisposition.String)
		}
		if retryClassifierVersion.Valid {
			retry.ClassifierVersion = int(retryClassifierVersion.Int32)
		}
		if retrySafeCode.Valid {
			retry.SafeCode = retrySafeCode.String
		}
		if retryUnmatchedCount.Valid {
			retry.UnmatchedCount = int(retryUnmatchedCount.Int32)
		}
		action.EmptyResponseRetry = retry
	}

	const membersQuery = `
		SELECT
			id,
			group_target_id,
			transfer_item_target_id,
			request_member_index,
			vendor_code,
			outcome_class,
			outcome_code,
			nm_id
		FROM wb.publication_action_members
		WHERE transfer_id = $1 AND action_id = $2
		ORDER BY request_member_index
		FOR UPDATE;
	`
	rows, err := tx.Query(ctx, membersQuery, candidate.TransferID, candidate.ActionID)
	if err != nil {
		return cardpublication_service.ProductAction{}, fmt.Errorf(
			"lock publication product action members: %w",
			err,
		)
	}
	defer rows.Close()
	for rows.Next() {
		var member cardpublication_service.ProductActionMember
		var outcomeClass, outcomeCode pgtype.Text
		var nmID pgtype.Int8
		if err := rows.Scan(
			&member.ID,
			&member.GroupTargetID,
			&member.TransferItemTargetID,
			&member.RequestMemberIndex,
			&member.VendorCode,
			&outcomeClass,
			&outcomeCode,
			&nmID,
		); err != nil {
			return cardpublication_service.ProductAction{}, fmt.Errorf(
				"scan publication product action member: %w",
				err,
			)
		}
		if outcomeClass.Valid {
			member.OutcomeClass = transfer_service.ResultClass(outcomeClass.String)
		}
		if outcomeCode.Valid {
			member.OutcomeCode = outcomeCode.String
		}
		if nmID.Valid {
			member.NMID = nmID.Int64
		}
		action.Members = append(action.Members, member)
	}
	if err := rows.Err(); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	if err := lockProductIdentities(ctx, tx, action); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	if err := action.Validate(); err != nil {
		return cardpublication_service.ProductAction{}, err
	}
	return action, nil
}

// lockProductIdentities блокирует строки product_identities для предотвращения гонок по вендор-кодам.
func lockProductIdentities(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_service.ProductAction,
) error {
	const query = `
		SELECT vendor_code_key
		FROM wb.product_identities
		WHERE cabinet_id = $1
			AND vendor_code_key = ANY($2::text[])
		ORDER BY vendor_code_key
		FOR UPDATE;
	`
	rows, err := tx.Query(ctx, query, action.CabinetID, action.VendorCodes())
	if err != nil {
		return fmt.Errorf("lock publication product identities: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ignored string
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(action.Members) {
		return cardpublication_service.ErrProductActionConflict
	}
	return nil
}
