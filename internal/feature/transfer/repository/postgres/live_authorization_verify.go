package transfer_postgres_repository

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5/pgtype"
)

// LockValidLive блокирует и верифицирует активную авторизацию для операции публикации.
func (repository *Repository) LockValidLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	check transfer_service.LiveAuthorizationCheck,
) (transfer_service.LiveAuthorizationEvidence, error) {
	authorization, err := lockLiveAuthorization(ctx, tx, check.AuthorizationID)
	if err != nil {
		return transfer_service.LiveAuthorizationEvidence{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_service.LiveAuthorizationEvidence{}, err
	}
	if expired {
		return transfer_service.LiveAuthorizationEvidence{}, transfer_service.ErrLiveExpired
	}
	if authorization.TransferID != check.TransferID ||
		authorization.Revision != check.ExpectedAuthorizationRevision ||
		authorization.State != transfer_service.LiveAuthorizationAuthorized ||
		authorization.PlanDigest != check.PlanDigest ||
		authorization.TargetSetRoot != check.TargetSetRoot ||
		authorization.ApprovedAt == nil {
		return transfer_service.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}

	const targetQuery = `
		SELECT
			target.cabinet_id,
			target.seller_key,
			target.client_generation,
			target.credential_expires_at,
			target.content_read,
			target.content_write,
			transfer.target_set_root,
			transfer.phase,
			transfer.outcome,
			action.state,
			action.authorization_id,
			plan.state,
			plan.plan_digest,
			plan.target_set_root,
			batch.source_kind,
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
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		JOIN wb.api_cabinets AS cabinet
		  ON cabinet.cabinet_id = target.cabinet_id
		JOIN wb.users AS owner
		  ON owner.tg_id = cabinet.owner_tg_id
		WHERE action.transfer_id = $1
			AND action.id = $2
			AND action.plan_id = $3
			AND action.target_id = $4
		FOR UPDATE OF transfer;
	`
	var (
		cabinetID        string
		sellerKey        []byte
		clientGeneration []byte
		expiresAt        time.Time
		contentRead      bool
		contentWrite     bool
		targetSetRoot    []byte
		phase            string
		outcome          string
		actionState      string
		actionAuthID     pgtype.Int8
		planState        string
		planDigest       []byte
		planTargetRoot   []byte
		batchSourceKind  string
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
		&cabinetID,
		&sellerKey,
		&clientGeneration,
		&expiresAt,
		&contentRead,
		&contentWrite,
		&targetSetRoot,
		&phase,
		&outcome,
		&actionState,
		&actionAuthID,
		&planState,
		&planDigest,
		&planTargetRoot,
		&batchSourceKind,
		&ownerRole,
	); err != nil {
		return transfer_service.LiveAuthorizationEvidence{}, fmt.Errorf(
			"lock live authorization target: %w",
			err,
		)
	}
	if cabinetID != string(check.CabinetID) ||
		!bytes.Equal(sellerKey, check.SellerKey[:]) ||
		!bytes.Equal(clientGeneration, check.ClientGeneration[:]) ||
		!bytes.Equal(targetSetRoot, check.TargetSetRoot[:]) ||
		!bytes.Equal(planDigest, check.PlanDigest[:]) ||
		!bytes.Equal(planTargetRoot, check.TargetSetRoot[:]) ||
		!expiresAt.After(now) || !contentRead || !contentWrite ||
		(phase != "preparing" && phase != "awaiting_authorization" && phase != "publishing" &&
			phase != "reconciling" && phase != "media") || outcome != "running" ||
		actionState != "planned" || actionAuthID.Valid ||
		(planState != "awaiting_authorization" && planState != "executing") {
		return transfer_service.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}
	if !targetOwnerMayMutate(batchSourceKind, ownerRole) {
		return transfer_service.LiveAuthorizationEvidence{}, transfer_service.ErrLiveAuthorization
	}
	return transfer_service.LiveAuthorizationEvidence{
		AuthorizationID:  authorization.ID,
		Revision:         authorization.Revision,
		TransferID:       authorization.TransferID,
		ActionID:         check.ActionID,
		PlanDigest:       authorization.PlanDigest,
		TargetSetRoot:    authorization.TargetSetRoot,
		TargetID:         check.TargetID,
		CabinetID:        check.CabinetID,
		SellerKey:        check.SellerKey,
		ClientGeneration: check.ClientGeneration,
		ActorDigest:      authorization.TrustedActorDigest,
		ApprovedAt:       *authorization.ApprovedAt,
		ExpiresAt:        authorization.ExpiresAt,
	}, nil
}

// targetOwnerMayMutate проверяет права владельца кабинета на модификацию.
func targetOwnerMayMutate(batchSourceKind, ownerRole string) bool {
	return batchSourceKind == "wb_cabinet" ||
		ownerRole == string(domain.RolePartner) ||
		ownerRole == string(domain.RoleAdmin)
}
