package transfer_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const liveAuthorizationColumns = `
	live_auth.id,
	live_auth.transfer_id,
	live_auth.plan_id,
	live_auth.revision,
	live_auth.state,
	live_auth.trusted_actor_id,
	live_auth.trusted_actor_name,
	live_auth.requested_at,
	live_auth.approved_at,
	live_auth.expires_at,
	live_auth.revoked_at,
	live_auth.closed_at,
	COALESCE(live_auth.safe_reason_code, ''),
	live_auth.created_at,
	live_auth.updated_at
`

func (repository *Repository) RequestLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	expiresAt time.Time,
	actor transfer_authorization.LiveTrustedActor,
	command transfer_authorization.RequestLiveCommand,
	plan transfer_authorization.AuthorizationPlanSummary,
) (transfer_authorization.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"request",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, planID: command.PlanID},
	); err != nil || found {
		return replay, err
	}
	open, found, err := lockOpenLiveAuthorization(ctx, tx, command.TransferID, plan.PlanID)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	if found {
		open, expired, err := expireLiveAuthorization(ctx, tx, now, open)
		if err != nil {
			return transfer_authorization.LiveAuthorization{}, err
		}
		if !expired {
			if open.PlanID != command.PlanID ||
				open.TrustedActorID != actor.TelegramUserID {
				return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
			}
			if err := insertLiveCommand(
				ctx,
				tx,
				open,
				"request",
				command.IdempotencyKey,
				liveCommandIdentity{actorID: actor.TelegramUserID, planID: command.PlanID},
			); err != nil {
				return transfer_authorization.LiveAuthorization{}, err
			}
			return open, nil
		}
	}

	const insert = `
		INSERT INTO wb.transfer_live_authorizations AS live_auth (
			transfer_id,
			plan_id,
			trusted_actor_id,
			trusted_actor_name,
			requested_at,
			expires_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5, $5)
		RETURNING ` + liveAuthorizationColumns + `;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(
		ctx,
		insert,
		command.TransferID,
		plan.PlanID,
		actor.TelegramUserID,
		actor.DisplayName,
		now,
		expiresAt,
	))
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, fmt.Errorf(
			"insert live authorization: %w",
			err,
		)
	}
	if err := insertLiveCommand(
		ctx,
		tx,
		authorization,
		"request",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, planID: command.PlanID},
	); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) ApproveLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	actor transfer_authorization.LiveTrustedActor,
	command transfer_authorization.ApproveLiveCommand,
) (transfer_authorization.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"approve",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, planID: command.ExpectedPlanID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision},
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	if expired {
		return authorization, nil
	}
	if authorization.TransferID != command.TransferID ||
		authorization.Revision != command.ExpectedRevision ||
		authorization.PlanID != command.ExpectedPlanID ||
		authorization.TrustedActorID != actor.TelegramUserID ||
		authorization.State != transfer_service.LiveAuthorizationRequested {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}

	const approve = `
		UPDATE wb.transfer_live_authorizations AS live_auth
		SET state = 'authorized',
		    approved_at = $2,
		    revision = live_auth.revision + 1,
		    updated_at = $2
		WHERE live_auth.id = $1
			AND live_auth.state = 'requested'
			AND live_auth.revision = $3
		RETURNING ` + liveAuthorizationColumns + `;
	`
	authorization, err = scanLiveAuthorization(tx.QueryRow(
		ctx,
		approve,
		command.AuthorizationID,
		now,
		command.ExpectedRevision,
	))
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, fmt.Errorf(
			"approve live authorization: %w",
			err,
		)
	}
	if err := insertLiveCommand(
		ctx,
		tx,
		authorization,
		"approve",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, planID: command.ExpectedPlanID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision},
	); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) RevokeLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	actor transfer_authorization.LiveTrustedActor,
	command transfer_authorization.RevokeLiveCommand,
) (transfer_authorization.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"revoke",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision, safeReasonCode: command.SafeReasonCode},
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	if expired {
		return authorization, nil
	}
	if authorization.TransferID != command.TransferID ||
		authorization.Revision != command.ExpectedRevision ||
		authorization.TrustedActorID != actor.TelegramUserID ||
		(authorization.State != transfer_service.LiveAuthorizationRequested &&
			authorization.State != transfer_service.LiveAuthorizationAuthorized) {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}

	const revoke = `
		UPDATE wb.transfer_live_authorizations AS live_auth
		SET state = 'revoked',
		    revoked_at = $2,
		    safe_reason_code = $3,
		    revision = live_auth.revision + 1,
		    updated_at = $2
		WHERE live_auth.id = $1
			AND live_auth.revision = $4
			AND live_auth.state IN ('requested', 'authorized')
		RETURNING ` + liveAuthorizationColumns + `;
	`
	authorization, err = scanLiveAuthorization(tx.QueryRow(
		ctx,
		revoke,
		command.AuthorizationID,
		now,
		command.SafeReasonCode,
		command.ExpectedRevision,
	))
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, fmt.Errorf(
			"revoke live authorization: %w",
			err,
		)
	}
	if err := insertLiveCommand(
		ctx,
		tx,
		authorization,
		"revoke",
		command.IdempotencyKey,
		liveCommandIdentity{actorID: actor.TelegramUserID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision, safeReasonCode: command.SafeReasonCode},
	); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) ChangeLiveState(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	state transfer_authorization.LiveAuthorizationState,
	command transfer_authorization.ChangeLiveAuthorizationStateCommand,
	idempotencyKey string,
) (transfer_authorization.LiveAuthorization, error) {
	if state != transfer_service.LiveAuthorizationSuperseded &&
		state != transfer_service.LiveAuthorizationClosed {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	kind, err := liveAuthorizationCommandKind(state)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		kind,
		idempotencyKey,
		liveCommandIdentity{planID: command.ExpectedPlanID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision, safeReasonCode: command.SafeReasonCode},
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	if state == transfer_service.LiveAuthorizationClosed &&
		(authorization.State == transfer_service.LiveAuthorizationClosed ||
			authorization.State == transfer_service.LiveAuthorizationRevoked ||
			authorization.State == transfer_service.LiveAuthorizationExpired ||
			authorization.State == transfer_service.LiveAuthorizationSuperseded) {
		if authorization.TransferID != command.TransferID ||
			authorization.PlanID != command.ExpectedPlanID ||
			(!expired && authorization.Revision != command.ExpectedRevision) {
			return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
		}
		return authorization, nil
	}
	if expired {
		return authorization, nil
	}
	allowed := authorization.State == transfer_service.LiveAuthorizationAuthorized
	if state == transfer_service.LiveAuthorizationSuperseded ||
		state == transfer_service.LiveAuthorizationClosed {
		allowed = allowed || authorization.State == transfer_service.LiveAuthorizationRequested
	}
	if authorization.TransferID != command.TransferID ||
		authorization.Revision != command.ExpectedRevision ||
		authorization.PlanID != command.ExpectedPlanID || !allowed {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}

	const change = `
		UPDATE wb.transfer_live_authorizations AS live_auth
		SET state = $2,
		    closed_at = $3,
		    safe_reason_code = $4,
		    revision = live_auth.revision + 1,
		    updated_at = $3
		WHERE live_auth.id = $1
			AND live_auth.revision = $5
			AND live_auth.state = ANY($6::text[])
			AND (
				($2 = 'superseded' AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_actions AS action
					JOIN wb.publication_attempts AS attempt
					  ON attempt.action_id = action.id
					WHERE action.transfer_id = live_auth.transfer_id
					  AND action.plan_id = live_auth.plan_id
				))
				OR
				($2 = 'closed' AND NOT EXISTS (
					SELECT 1
					FROM wb.publication_actions AS action
					WHERE action.transfer_id = live_auth.transfer_id
					  AND action.plan_id = live_auth.plan_id
					  AND action.state NOT IN ('terminal', 'superseded')
				))
			)
		RETURNING ` + liveAuthorizationColumns + `;
	`
	allowedStates := []string{string(transfer_service.LiveAuthorizationAuthorized)}
	if state == transfer_service.LiveAuthorizationSuperseded ||
		state == transfer_service.LiveAuthorizationClosed {
		allowedStates = append(
			allowedStates,
			string(transfer_service.LiveAuthorizationRequested),
		)
	}
	authorization, err = scanLiveAuthorization(tx.QueryRow(
		ctx,
		change,
		command.AuthorizationID,
		state,
		now,
		command.SafeReasonCode,
		command.ExpectedRevision,
		allowedStates,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, fmt.Errorf(
			"change live authorization state: %w",
			err,
		)
	}
	if err := insertLiveCommand(
		ctx,
		tx,
		authorization,
		kind,
		idempotencyKey,
		liveCommandIdentity{planID: command.ExpectedPlanID, authorizationID: command.AuthorizationID, expectedRevision: command.ExpectedRevision, safeReasonCode: command.SafeReasonCode},
	); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	return authorization, nil
}

func liveAuthorizationCommandKind(
	state transfer_authorization.LiveAuthorizationState,
) (string, error) {
	switch state {
	case transfer_service.LiveAuthorizationSuperseded:
		return "supersede", nil
	case transfer_service.LiveAuthorizationClosed:
		return "close", nil
	default:
		return "", transfer_service.ErrLiveAuthorization
	}
}

type liveCommandIdentity struct {
	actorID          int64
	planID           int64
	authorizationID  transfer_authorization.LiveAuthorizationID
	expectedRevision int64
	safeReasonCode   string
}

func loadLiveCommandReplay(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	kind string,
	idempotencyKey string,
	identity liveCommandIdentity,
) (transfer_authorization.LiveAuthorization, bool, error) {
	const query = `
		SELECT
			command.kind,
			command.actor_id,
			command.plan_id,
			command.expected_revision,
			command.safe_reason_code,
			` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorization_commands AS command
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.id = command.authorization_id
		WHERE command.idempotency_key = $1
		FOR UPDATE OF live_auth;
	`
	var storedKind string
	var storedActorID, storedPlanID, storedRevision int64
	var storedReasonCode string
	row := tx.QueryRow(ctx, query, idempotencyKey)
	destinations := []any{&storedKind, &storedActorID, &storedPlanID, &storedRevision, &storedReasonCode}
	authorization, err := scanLiveAuthorizationWithPrefix(row, destinations)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_authorization.LiveAuthorization{}, false, nil
	}
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, false, fmt.Errorf(
			"load live command replay: %w",
			err,
		)
	}
	if storedKind != kind || storedActorID != identity.actorID ||
		storedPlanID != identity.planID || storedRevision != identity.expectedRevision ||
		storedReasonCode != identity.safeReasonCode ||
		(identity.authorizationID > 0 && authorization.ID != identity.authorizationID) {
		return transfer_authorization.LiveAuthorization{}, true, transfer_service.ErrLiveIdempotency
	}
	authorization, _, err = expireLiveAuthorization(ctx, tx, now, authorization)
	return authorization, true, err
}

func insertLiveCommand(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	authorization transfer_authorization.LiveAuthorization,
	kind string,
	idempotencyKey string,
	identity liveCommandIdentity,
) error {
	const insert = `
		INSERT INTO wb.transfer_live_authorization_commands (
			transfer_id,
			authorization_id,
			kind,
			idempotency_key,
			actor_id,
			plan_id,
			expected_revision,
			safe_reason_code,
			result_state,
			result_revision
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
	`
	if _, err := tx.Exec(
		ctx,
		insert,
		authorization.TransferID,
		authorization.ID,
		kind,
		idempotencyKey,
		identity.actorID,
		identity.planID,
		identity.expectedRevision,
		identity.safeReasonCode,
		authorization.State,
		authorization.Revision,
	); err != nil {
		return fmt.Errorf("insert live authorization command: %w", err)
	}
	return nil
}

func lockLiveAuthorization(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	authorizationID transfer_authorization.LiveAuthorizationID,
) (transfer_authorization.LiveAuthorization, error) {
	query := `
		SELECT ` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorizations AS live_auth
		WHERE live_auth.id = $1
		FOR UPDATE;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(ctx, query, authorizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_authorization.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, fmt.Errorf(
			"lock live authorization: %w",
			err,
		)
	}
	return authorization, nil
}

func lockOpenLiveAuthorization(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_model.TransferID,
	planID int64,
) (transfer_authorization.LiveAuthorization, bool, error) {
	query := `
		SELECT ` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorizations AS live_auth
		WHERE live_auth.transfer_id = $1
			AND live_auth.plan_id = $2
			AND live_auth.state IN ('requested', 'authorized')
		FOR UPDATE;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(ctx, query, transferID, planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_authorization.LiveAuthorization{}, false, nil
	}
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, false, fmt.Errorf(
			"lock open live authorization: %w",
			err,
		)
	}
	return authorization, true, nil
}

func expireLiveAuthorization(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	authorization transfer_authorization.LiveAuthorization,
) (transfer_authorization.LiveAuthorization, bool, error) {
	if authorization.ExpiresAt.After(now) ||
		(authorization.State != transfer_service.LiveAuthorizationRequested &&
			authorization.State != transfer_service.LiveAuthorizationAuthorized) {
		return authorization, false, nil
	}
	const expire = `
		UPDATE wb.transfer_live_authorizations AS live_auth
		SET state = 'expired',
		    safe_reason_code = 'authorization_ttl_expired',
		    revision = live_auth.revision + 1,
		    updated_at = $2
		WHERE live_auth.id = $1
			AND live_auth.state IN ('requested', 'authorized')
		RETURNING ` + liveAuthorizationColumns + `;
	`
	expired, err := scanLiveAuthorization(tx.QueryRow(ctx, expire, authorization.ID, now))
	if err != nil {
		return transfer_authorization.LiveAuthorization{}, false, fmt.Errorf(
			"expire live authorization: %w",
			err,
		)
	}
	if err := insertSystemExpireCommand(ctx, tx, authorization, expired); err != nil {
		return transfer_authorization.LiveAuthorization{}, false, err
	}
	return expired, true, nil
}

func insertSystemExpireCommand(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	previous transfer_authorization.LiveAuthorization,
	expired transfer_authorization.LiveAuthorization,
) error {
	return insertLiveCommand(
		ctx,
		tx,
		expired,
		"expire",
		fmt.Sprintf("system:expire-live:%d:%d", previous.ID, previous.Revision),
		liveCommandIdentity{authorizationID: previous.ID, expectedRevision: previous.Revision},
	)
}

func scanLiveAuthorization(row rowScanner) (transfer_authorization.LiveAuthorization, error) {
	return scanLiveAuthorizationWithPrefix(row, nil)
}

func scanLiveAuthorizationWithPrefix(
	row rowScanner,
	prefix []any,
) (transfer_authorization.LiveAuthorization, error) {
	var (
		authorization                   transfer_authorization.LiveAuthorization
		id, transferID                  int64
		approvedAt, revokedAt, closedAt pgtype.Timestamptz
	)
	destinations := append(prefix,
		&id,
		&transferID,
		&authorization.PlanID,
		&authorization.Revision,
		&authorization.State,
		&authorization.TrustedActorID,
		&authorization.TrustedActorName,
		&authorization.RequestedAt,
		&approvedAt,
		&authorization.ExpiresAt,
		&revokedAt,
		&closedAt,
		&authorization.SafeReasonCode,
		&authorization.CreatedAt,
		&authorization.UpdatedAt,
	)
	if err := row.Scan(destinations...); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	authorization.ID = transfer_authorization.LiveAuthorizationID(id)
	authorization.TransferID = transfer_model.TransferID(transferID)
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		authorization.ApprovedAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		authorization.RevokedAt = &value
	}
	if closedAt.Valid {
		value := closedAt.Time.UTC()
		authorization.ClosedAt = &value
	}
	authorization.RequestedAt = authorization.RequestedAt.UTC()
	authorization.ExpiresAt = authorization.ExpiresAt.UTC()
	authorization.CreatedAt = authorization.CreatedAt.UTC()
	authorization.UpdatedAt = authorization.UpdatedAt.UTC()
	if err := authorization.Validate(); err != nil {
		return transfer_authorization.LiveAuthorization{}, err
	}
	return authorization, nil
}

var _ transfer_authorization.LiveAuthorizationRepository = (*Repository)(nil)
