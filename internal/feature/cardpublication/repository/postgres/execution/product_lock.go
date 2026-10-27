package execution

import (
	"context"
	"errors"
	"fmt"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// LockProductAction блокирует действие с товаром и его элементы для исполнения.
func (repository *ExecutionRepository) LockProductAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	candidate cardpublication_execution.ProductActionCandidate,
) (cardpublication_execution.ProductAction, error) {
	if tx == nil || candidate.TransferID <= 0 || candidate.PlanID <= 0 || candidate.ActionID <= 0 ||
		candidate.AuthorizationID <= 0 {
		return cardpublication_execution.ProductAction{}, errors.New(
			"lock publication product action: identity is invalid",
		)
	}
	const query = `
		SELECT
			action.plan_id,
			action.revision,
			action.kind,
			action.state,
			action.request_payload,
			target.seller_id,
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
			retry.request_payload,
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
			AND target.seller_id = $4
			AND plan.id = $6
		FOR UPDATE OF action, plan;
	`
	action := cardpublication_execution.ProductAction{ProductActionCandidate: candidate}
	var (
		generation                   int64
		sellerID                     core_domain.SellerID
		preflightID                  pgtype.Int8
		attemptID, recheckID         pgtype.Int8
		attemptStartedAt             pgtype.Timestamptz
		attemptFinishedAt            pgtype.Timestamptz
		attemptDelivery, attemptCode pgtype.Text
		attemptHTTPStatus            pgtype.Int4
		retryID                      pgtype.Int8
		retryPayload                 []byte
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
		candidate.SellerID,
		candidate.AuthorizationID,
		candidate.PlanID,
	).Scan(
		&action.PlanID,
		&action.Revision,
		&action.Kind,
		&action.State,
		&action.RequestPayload,
		&sellerID,
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
		&retryPayload,
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
		return cardpublication_execution.ProductAction{}, cardpublication_execution.ErrProductActionConflict
	}
	if err != nil {
		return cardpublication_execution.ProductAction{}, fmt.Errorf(
			"lock publication product action: %w",
			err,
		)
	}
	if sellerID.Validate() != nil ||
		generation <= 0 || !preflightID.Valid {
		return cardpublication_execution.ProductAction{}, cardpublication_execution.ErrProductActionConflict
	}
	action.SellerID = sellerID
	action.ClientGeneration = core_domain.ClientGeneration(generation)
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
		action.AttemptDelivery = cardpublication_execution.SubmissionDelivery(attemptDelivery.String)
	}
	if attemptHTTPStatus.Valid {
		action.AttemptHTTPStatus = int(attemptHTTPStatus.Int32)
	}
	if attemptCode.Valid {
		action.AttemptSafeCode = attemptCode.String
	}
	if retryID.Valid {
		if len(retryPayload) == 0 || !retryStartedAt.Valid {
			return cardpublication_execution.ProductAction{}, cardpublication_execution.ErrProductActionConflict
		}
		retry := cardpublication_execution.ProductRetry{
			ID:              retryID.Int64,
			TransferID:      action.TransferID,
			ActionID:        action.ActionID,
			AuthorizationID: action.AuthorizationID,
			StartedAt:       retryStartedAt.Time.UTC(),
		}
		retry.RequestPayload = append([]byte(nil), retryPayload...)
		if retryFinishedAt.Valid {
			retry.FinishedAt = retryFinishedAt.Time.UTC()
		}
		if retryDelivery.Valid {
			retry.Delivery = cardpublication_execution.SubmissionDelivery(retryDelivery.String)
		}
		if retryHTTPStatus.Valid {
			retry.HTTPStatus = int(retryHTTPStatus.Int32)
		}
		if retryDisposition.Valid {
			retry.Disposition = cardpublication_execution.SubmissionDisposition(retryDisposition.String)
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
		return cardpublication_execution.ProductAction{}, fmt.Errorf(
			"lock publication product action members: %w",
			err,
		)
	}
	defer rows.Close()
	for rows.Next() {
		var member cardpublication_execution.ProductActionMember
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
			return cardpublication_execution.ProductAction{}, fmt.Errorf(
				"scan publication product action member: %w",
				err,
			)
		}
		if outcomeClass.Valid {
			member.OutcomeClass = transfer_model.ResultClass(outcomeClass.String)
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
		return cardpublication_execution.ProductAction{}, err
	}
	if err := lockProductIdentities(ctx, tx, action); err != nil {
		return cardpublication_execution.ProductAction{}, err
	}
	if err := action.Validate(); err != nil {
		return cardpublication_execution.ProductAction{}, err
	}
	return action, nil
}

// lockProductIdentities блокирует строки product_identities для предотвращения гонок по вендор-кодам.
func lockProductIdentities(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	action cardpublication_execution.ProductAction,
) error {
	const query = `
		SELECT vendor_code_key
		FROM wb.product_identities
		WHERE seller_id = $1
			AND vendor_code_key = ANY($2::text[])
		ORDER BY vendor_code_key
		FOR UPDATE;
	`
	rows, err := tx.Query(ctx, query, action.SellerID, action.VendorCodes())
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
		return cardpublication_execution.ErrProductActionConflict
	}
	return nil
}
