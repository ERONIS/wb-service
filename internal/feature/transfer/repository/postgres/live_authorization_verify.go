package transfer_postgres_repository

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"

	"github.com/jackc/pgx/v5/pgtype"
)

// LockValidLive блокирует и верифицирует активную авторизацию для операции публикации.
func (repository *Repository) LockValidLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	check transfer_authorization.LiveAuthorizationCheck,
) (transfer_authorization.LiveAuthorizationEvidence, error) {
	authorization, err := lockLiveAuthorization(ctx, tx, check.AuthorizationID)
	if err != nil {
		return transfer_authorization.LiveAuthorizationEvidence{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_authorization.LiveAuthorizationEvidence{}, err
	}
	if expired {
		return transfer_authorization.LiveAuthorizationEvidence{}, transfer_service.ErrLiveExpired
	}
	if authorization.TransferID != check.TransferID ||
		authorization.Revision != check.ExpectedAuthorizationRevision ||
		authorization.State != transfer_service.LiveAuthorizationAuthorized ||
		authorization.PlanID != check.PlanID ||
		authorization.ApprovedAt == nil {
		return transfer_authorization.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}

	const targetQuery = `
		SELECT
			target.seller_id,
			target.client_generation,
			target.credential_expires_at,
			target.content_read,
			target.content_write,
			transfer.phase,
			transfer.outcome,
			action.state,
			action.authorization_id,
			plan.state,
			owner.role
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfers AS transfer
		  ON transfer.id = action.transfer_id
		JOIN wb.api_cabinets AS cabinet
		  ON cabinet.seller_id = target.seller_id
		JOIN wb.users AS owner
		  ON owner.tg_id = cabinet.owner_tg_id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.plan_id = $3
			AND action.target_id = $4
		FOR UPDATE OF transfer;
	`
	var (
		sellerID         string
		clientGeneration []byte
		expiresAt        time.Time
		contentRead      bool
		contentWrite     bool
		phase            string
		outcome          string
		actionState      string
		actionAuthID     pgtype.Int8
		planState        string
		ownerRole        string
	)
	if err := tx.QueryRow(
		ctx,
		targetQuery,
		check.TransferID,
		check.ActionID,
		authorization.PlanID,
		check.TargetID,
	).Scan(
		&sellerID,
		&clientGeneration,
		&expiresAt,
		&contentRead,
		&contentWrite,
		&phase,
		&outcome,
		&actionState,
		&actionAuthID,
		&planState,
		&ownerRole,
	); err != nil {
		return transfer_authorization.LiveAuthorizationEvidence{}, fmt.Errorf(
			"lock live authorization target: %w",
			err,
		)
	}
	if sellerID != string(check.SellerID) ||
		!bytes.Equal(clientGeneration, check.ClientGeneration[:]) ||
		!expiresAt.After(now) || !contentRead || !contentWrite ||
		(phase != "preparing" && phase != "awaiting_authorization" && phase != "publishing" &&
			phase != "reconciling" && phase != "media") || outcome != "running" ||
		actionState != "planned" || actionAuthID.Valid ||
		(planState != "awaiting_authorization" && planState != "executing") {
		return transfer_authorization.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}
	if !targetOwnerMayMutate(ownerRole) {
		return transfer_authorization.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}
	return transfer_authorization.LiveAuthorizationEvidence{
		AuthorizationID:  authorization.ID,
		Revision:         authorization.Revision,
		TransferID:       authorization.TransferID,
		ActionID:         check.ActionID,
		PlanID:           authorization.PlanID,
		TargetID:         check.TargetID,
		SellerID:         check.SellerID,
		ClientGeneration: check.ClientGeneration,
		ActorID:          authorization.TrustedActorID,
		ApprovedAt:       *authorization.ApprovedAt,
		ExpiresAt:        authorization.ExpiresAt,
	}, nil
}

// targetOwnerMayMutate проверяет права владельца кабинета на модификацию.
func targetOwnerMayMutate(ownerRole string) bool {
	return ownerRole == string(domain.RolePartner) ||
		ownerRole == string(domain.RoleAdmin)
}
