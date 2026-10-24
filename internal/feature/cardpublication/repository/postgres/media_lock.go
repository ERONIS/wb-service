package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// LockMediaAction блокирует медиа-действие для исполнения и валидирует его состояние.
func (repository *Repository) LockMediaAction(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	candidate cardpublication_execution.ProductActionCandidate,
) (cardpublication_execution.MediaAction, error) {
	if tx == nil || candidate.TransferID <= 0 || candidate.PlanID <= 0 || candidate.ActionID <= 0 ||
		candidate.AuthorizationID <= 0 {
		return cardpublication_execution.MediaAction{},
			errors.New("lock publication media action: identity is invalid")
	}
	const query = `
		SELECT
			action.plan_id,
			action.revision,
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
			media_member.id,
			media_member.group_target_id,
			media_member.transfer_item_target_id,
			media_member.request_member_index,
			media_member.vendor_code,
			attribution.id,
			COALESCE(attribution.nm_id, identity.nm_id),
			identity.revision,
			attempt.id,
			attempt.recheck_observation_id,
			attempt.request_payload,
			attempt.started_at,
			live_auth.revision,
			(
				SELECT COUNT(*)
				FROM wb.publication_action_members AS counted_member
				WHERE counted_member.transfer_id = action.transfer_id
				  AND counted_member.action_id = action.id
			),
			(
				SELECT COUNT(*)
				FROM wb.publication_action_members AS counted_product_member
				JOIN wb.publication_actions AS counted_product_action
				  ON counted_product_action.transfer_id
				     = counted_product_member.transfer_id
				 AND counted_product_action.id = counted_product_member.action_id
				JOIN wb.publication_attributions AS counted_attribution
				  ON counted_attribution.transfer_id
				     = counted_product_member.transfer_id
				 AND counted_attribution.action_member_id = counted_product_member.id
				 AND counted_attribution.level IN ('direct', 'observed_after_attempt')
				WHERE counted_product_member.transfer_id = media_member.transfer_id
				  AND counted_product_member.transfer_item_target_id
				      = media_member.transfer_item_target_id
				  AND counted_product_action.plan_id = action.plan_id
				  AND counted_attribution.seller_id = target.seller_id
				  AND counted_attribution.group_target_id = media_member.group_target_id
			),
			product_action.id IS NOT NULL
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
		JOIN wb.publication_action_members AS media_member
		  ON media_member.transfer_id = action.transfer_id
		 AND media_member.action_id = action.id
		LEFT JOIN wb.publication_action_members AS product_member
		  ON product_member.transfer_id = media_member.transfer_id
		 AND product_member.transfer_item_target_id
		     = media_member.transfer_item_target_id
		LEFT JOIN wb.publication_actions AS product_action
		  ON product_action.transfer_id = product_member.transfer_id
		 AND product_action.id = product_member.action_id
		 AND product_action.plan_id = action.plan_id
		LEFT JOIN wb.publication_attributions AS attribution
		  ON attribution.transfer_id = product_member.transfer_id
		 AND attribution.action_member_id = product_member.id
		 AND attribution.level IN ('direct', 'observed_after_attempt')
		 AND attribution.seller_id = target.seller_id
		 AND attribution.group_target_id = media_member.group_target_id
		JOIN wb.product_identities AS identity
		  ON identity.seller_id = target.seller_id
		 AND identity.vendor_code_key = media_member.vendor_code
		 AND (attribution.nm_id IS NULL OR identity.nm_id = attribution.nm_id)
		LEFT JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.target_id = $3
			AND target.seller_id = $4
			AND action.kind = 'upload_media'
			AND (
				(product_action.id IS NOT NULL
				 AND product_action.kind IN ('create_group', 'add_to_group')
				 AND product_action.state = 'terminal')
				OR
				product_action.id IS NULL
			)
			AND plan.id = $6
			AND identity.state = 'remote_present'
			AND identity.nm_id IS NOT NULL
			AND (
				(action.state = 'planned'
				 AND action.authorization_id IS NULL
				 AND identity.active_action_id IS NULL)
				OR
				(action.state IN ('dispatching', 'reconciling')
				 AND action.authorization_id = live_auth.id
				 AND identity.active_transfer_id = action.transfer_id
				 AND identity.active_action_id = action.id)
			)
		FOR UPDATE OF action, plan, media_member, identity;
	`
	action := cardpublication_execution.MediaAction{ProductActionCandidate: candidate}
	var (
		generation                    []byte
		sellerID                      core_domain.SellerID
		preflightID                   pgtype.Int8
		attemptID, recheckID          pgtype.Int8
		attributionID                 pgtype.Int8
		attemptRequestPayload         []byte
		attemptStartedAt              pgtype.Timestamptz
		currentAuthorizationRevision  int64
		memberCount, attributionCount int64
		hasProductAction              bool
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
		&action.State,
		&action.RequestPayload,
		&sellerID,
		&generation,
		&action.CredentialExpiresAt,
		&preflightID,
		&action.Member.ID,
		&action.Member.GroupTargetID,
		&action.Member.TransferItemTargetID,
		&action.Member.RequestMemberIndex,
		&action.Member.VendorCode,
		&attributionID,
		&action.NMID,
		&action.IdentityRevision,
		&attemptID,
		&recheckID,
		&attemptRequestPayload,
		&attemptStartedAt,
		&currentAuthorizationRevision,
		&memberCount,
		&attributionCount,
		&hasProductAction,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardpublication_execution.MediaAction{},
			cardpublication_service.ErrMediaActionConflict
	}
	if err != nil {
		return cardpublication_execution.MediaAction{},
			fmt.Errorf("lock publication media action: %w", err)
	}
	if sellerID.Validate() != nil ||
		len(generation) != len(action.ClientGeneration) || !preflightID.Valid ||
		memberCount != 1 || (hasProductAction && attributionCount != 1) || (!hasProductAction && attributionCount != 0) {
		return cardpublication_execution.MediaAction{},
			cardpublication_service.ErrMediaActionConflict
	}
	if attributionID.Valid {
		action.AttributionID = attributionID.Int64
	}
	action.SellerID = sellerID
	copy(action.ClientGeneration[:], generation)
	action.PreflightObservationID = preflightID.Int64
	action.AuthorizationRevision = currentAuthorizationRevision
	if attemptID.Valid {
		action.AttemptID = attemptID.Int64
	}
	if recheckID.Valid {
		action.RecheckObservationID = recheckID.Int64
	}
	if len(attemptRequestPayload) > 0 {
		action.AttemptRequestPayload = append([]byte(nil), attemptRequestPayload...)
	}
	if attemptStartedAt.Valid {
		action.AttemptStartedAt = attemptStartedAt.Time.UTC()
	}
	if err := action.Validate(); err != nil {
		return cardpublication_execution.MediaAction{}, err
	}
	return action, nil
}
