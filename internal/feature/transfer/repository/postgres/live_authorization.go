package transfer_postgres_repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const liveAuthorizationColumns = `
	live_auth.id,
	live_auth.transfer_id,
	live_auth.plan_id,
	live_auth.plan_digest,
	live_auth.target_set_root,
	live_auth.revision,
	live_auth.state,
	live_auth.trusted_actor_id,
	live_auth.trusted_actor_digest,
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
	actor transfer_service.LiveTrustedActor,
	actorDigest transfer_service.Digest,
	command transfer_service.RequestLiveCommand,
	commandDigest transfer_service.Digest,
	plan transfer_service.AuthorizationPlanSummary,
) (transfer_service.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"request",
		command.IdempotencyKey,
		actorDigest,
		commandDigest,
	); err != nil || found {
		return replay, err
	}
	open, found, err := lockOpenLiveAuthorization(ctx, tx, command.TransferID)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	if found {
		open, expired, err := expireLiveAuthorization(ctx, tx, now, open)
		if err != nil {
			return transfer_service.LiveAuthorization{}, err
		}
		if !expired {
			if open.PlanDigest != command.PlanDigest ||
				open.TrustedActorID != actor.TelegramUserID ||
				open.TrustedActorDigest != actorDigest {
				return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
			}
			if err := insertLiveCommand(
				ctx,
				tx,
				open,
				"request",
				command.IdempotencyKey,
				actorDigest,
				commandDigest,
			); err != nil {
				return transfer_service.LiveAuthorization{}, err
			}
			return open, nil
		}
	}

	const insert = `
		INSERT INTO wb.transfer_live_authorizations AS live_auth (
			transfer_id,
			plan_id,
			plan_digest,
			target_set_root,
			trusted_actor_id,
			trusted_actor_digest,
			trusted_actor_name,
			requested_at,
			expires_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $8, $8)
		RETURNING ` + liveAuthorizationColumns + `;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(
		ctx,
		insert,
		command.TransferID,
		plan.PlanID,
		command.PlanDigest[:],
		plan.TargetSetRoot[:],
		actor.TelegramUserID,
		actorDigest[:],
		actor.DisplayName,
		now,
		expiresAt,
	))
	if err != nil {
		return transfer_service.LiveAuthorization{}, fmt.Errorf(
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
		actorDigest,
		commandDigest,
	); err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) ApproveLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	actor transfer_service.LiveTrustedActor,
	actorDigest transfer_service.Digest,
	command transfer_service.ApproveLiveCommand,
	commandDigest transfer_service.Digest,
) (transfer_service.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"approve",
		command.IdempotencyKey,
		actorDigest,
		commandDigest,
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	if expired {
		return authorization, nil
	}
	if authorization.TransferID != command.TransferID ||
		authorization.Revision != command.ExpectedRevision ||
		authorization.PlanDigest != command.ExpectedPlanDigest ||
		authorization.TrustedActorID != actor.TelegramUserID ||
		authorization.TrustedActorDigest != actorDigest ||
		authorization.State != transfer_service.LiveAuthorizationRequested {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
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
		return transfer_service.LiveAuthorization{}, fmt.Errorf(
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
		actorDigest,
		commandDigest,
	); err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) RevokeLive(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	actor transfer_service.LiveTrustedActor,
	actorDigest transfer_service.Digest,
	command transfer_service.RevokeLiveCommand,
	commandDigest transfer_service.Digest,
) (transfer_service.LiveAuthorization, error) {
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		"revoke",
		command.IdempotencyKey,
		actorDigest,
		commandDigest,
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	if expired {
		return authorization, nil
	}
	if authorization.TransferID != command.TransferID ||
		authorization.Revision != command.ExpectedRevision ||
		authorization.TrustedActorID != actor.TelegramUserID ||
		authorization.TrustedActorDigest != actorDigest ||
		(authorization.State != transfer_service.LiveAuthorizationRequested &&
			authorization.State != transfer_service.LiveAuthorizationAuthorized) {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
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
		return transfer_service.LiveAuthorization{}, fmt.Errorf(
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
		actorDigest,
		commandDigest,
	); err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	return authorization, nil
}

func (repository *Repository) ChangeLiveState(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	state transfer_service.LiveAuthorizationState,
	command transfer_service.ChangeLiveAuthorizationStateCommand,
	actorDigest transfer_service.Digest,
	commandDigest transfer_service.Digest,
	idempotencyKey string,
) (transfer_service.LiveAuthorization, error) {
	if state != transfer_service.LiveAuthorizationSuperseded &&
		state != transfer_service.LiveAuthorizationClosed {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	kind, err := liveAuthorizationCommandKind(state)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	if replay, found, err := loadLiveCommandReplay(
		ctx,
		tx,
		now,
		kind,
		idempotencyKey,
		actorDigest,
		commandDigest,
	); err != nil || found {
		return replay, err
	}
	authorization, err := lockLiveAuthorization(ctx, tx, command.AuthorizationID)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	authorization, expired, err := expireLiveAuthorization(ctx, tx, now, authorization)
	if err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	if state == transfer_service.LiveAuthorizationClosed &&
		(authorization.State == transfer_service.LiveAuthorizationClosed ||
			authorization.State == transfer_service.LiveAuthorizationRevoked ||
			authorization.State == transfer_service.LiveAuthorizationExpired ||
			authorization.State == transfer_service.LiveAuthorizationSuperseded) {
		if authorization.TransferID != command.TransferID ||
			authorization.PlanDigest != command.ExpectedPlanDigest ||
			(!expired && authorization.Revision != command.ExpectedRevision) {
			return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
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
		authorization.PlanDigest != command.ExpectedPlanDigest || !allowed {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
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
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	if err != nil {
		return transfer_service.LiveAuthorization{}, fmt.Errorf(
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
		actorDigest,
		commandDigest,
	); err != nil {
		return transfer_service.LiveAuthorization{}, err
	}
	return authorization, nil
}

func liveAuthorizationCommandKind(
	state transfer_service.LiveAuthorizationState,
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
			plan.target_set_root
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfers AS transfer
		  ON transfer.id = action.transfer_id
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
		(phase != "awaiting_authorization" && phase != "publishing" &&
			phase != "reconciling" && phase != "media") || outcome != "running" ||
		actionState != "planned" || actionAuthID.Valid ||
		(planState != "awaiting_authorization" && planState != "executing") {
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

func loadLiveCommandReplay(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	now time.Time,
	kind string,
	idempotencyKey string,
	actorDigest transfer_service.Digest,
	commandDigest transfer_service.Digest,
) (transfer_service.LiveAuthorization, bool, error) {
	const query = `
		SELECT
			command.kind,
			command.actor_digest,
			command.command_digest,
			` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorization_commands AS command
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.id = command.authorization_id
		WHERE command.idempotency_key = $1
		FOR UPDATE OF live_auth;
	`
	var storedKind string
	var storedActorDigest, storedCommandDigest []byte
	row := tx.QueryRow(ctx, query, idempotencyKey)
	destinations := []any{&storedKind, &storedActorDigest, &storedCommandDigest}
	authorization, err := scanLiveAuthorizationWithPrefix(row, destinations)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.LiveAuthorization{}, false, nil
	}
	if err != nil {
		return transfer_service.LiveAuthorization{}, false, fmt.Errorf(
			"load live command replay: %w",
			err,
		)
	}
	if storedKind != kind || !bytes.Equal(storedActorDigest, actorDigest[:]) ||
		!bytes.Equal(storedCommandDigest, commandDigest[:]) {
		return transfer_service.LiveAuthorization{}, true, transfer_service.ErrLiveIdempotency
	}
	authorization, _, err = expireLiveAuthorization(ctx, tx, now, authorization)
	return authorization, true, err
}

func insertLiveCommand(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	authorization transfer_service.LiveAuthorization,
	kind string,
	idempotencyKey string,
	actorDigest transfer_service.Digest,
	commandDigest transfer_service.Digest,
) error {
	const insert = `
		INSERT INTO wb.transfer_live_authorization_commands (
			transfer_id,
			authorization_id,
			kind,
			idempotency_key,
			actor_digest,
			command_digest,
			result_state,
			result_revision
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
	`
	if _, err := tx.Exec(
		ctx,
		insert,
		authorization.TransferID,
		authorization.ID,
		kind,
		idempotencyKey,
		actorDigest[:],
		commandDigest[:],
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
	authorizationID transfer_service.LiveAuthorizationID,
) (transfer_service.LiveAuthorization, error) {
	query := `
		SELECT ` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorizations AS live_auth
		WHERE live_auth.id = $1
		FOR UPDATE;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(ctx, query, authorizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	if err != nil {
		return transfer_service.LiveAuthorization{}, fmt.Errorf(
			"lock live authorization: %w",
			err,
		)
	}
	return authorization, nil
}

func lockOpenLiveAuthorization(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID transfer_service.TransferID,
) (transfer_service.LiveAuthorization, bool, error) {
	query := `
		SELECT ` + liveAuthorizationColumns + `
		FROM wb.transfer_live_authorizations AS live_auth
		WHERE live_auth.transfer_id = $1
			AND live_auth.state IN ('requested', 'authorized')
		FOR UPDATE;
	`
	authorization, err := scanLiveAuthorization(tx.QueryRow(ctx, query, transferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.LiveAuthorization{}, false, nil
	}
	if err != nil {
		return transfer_service.LiveAuthorization{}, false, fmt.Errorf(
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
	authorization transfer_service.LiveAuthorization,
) (transfer_service.LiveAuthorization, bool, error) {
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
		return transfer_service.LiveAuthorization{}, false, fmt.Errorf(
			"expire live authorization: %w",
			err,
		)
	}
	if err := insertSystemExpireCommand(ctx, tx, authorization, expired); err != nil {
		return transfer_service.LiveAuthorization{}, false, err
	}
	return expired, true, nil
}

func insertSystemExpireCommand(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	previous transfer_service.LiveAuthorization,
	expired transfer_service.LiveAuthorization,
) error {
	actorBytes := sha256.Sum256([]byte("transfer-system-actor:v1"))
	commandBytes := sha256.Sum256([]byte(fmt.Sprintf(
		"transfer-expire-live:v1:%d:%d:%s",
		previous.ID,
		previous.Revision,
		previous.ExpiresAt.UTC().Format(time.RFC3339Nano),
	)))
	var actorDigest, commandDigest transfer_service.Digest
	copy(actorDigest[:], actorBytes[:])
	copy(commandDigest[:], commandBytes[:])
	return insertLiveCommand(
		ctx,
		tx,
		expired,
		"expire",
		fmt.Sprintf("system:expire-live:%d:%d", previous.ID, previous.Revision),
		actorDigest,
		commandDigest,
	)
}

func scanLiveAuthorization(row rowScanner) (transfer_service.LiveAuthorization, error) {
	return scanLiveAuthorizationWithPrefix(row, nil)
}

func scanLiveAuthorizationWithPrefix(
	row rowScanner,
	prefix []any,
) (transfer_service.LiveAuthorization, error) {
	var (
		authorization                   transfer_service.LiveAuthorization
		id, transferID                  int64
		planDigest, targetSetRoot       []byte
		actorDigest                     []byte
		approvedAt, revokedAt, closedAt pgtype.Timestamptz
	)
	destinations := append(prefix,
		&id,
		&transferID,
		&authorization.PlanID,
		&planDigest,
		&targetSetRoot,
		&authorization.Revision,
		&authorization.State,
		&authorization.TrustedActorID,
		&actorDigest,
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
		return transfer_service.LiveAuthorization{}, err
	}
	if len(planDigest) != len(authorization.PlanDigest) ||
		len(targetSetRoot) != len(authorization.TargetSetRoot) ||
		len(actorDigest) != len(authorization.TrustedActorDigest) {
		return transfer_service.LiveAuthorization{}, transfer_service.ErrLiveAuthorization
	}
	authorization.ID = transfer_service.LiveAuthorizationID(id)
	authorization.TransferID = transfer_service.TransferID(transferID)
	copy(authorization.PlanDigest[:], planDigest)
	copy(authorization.TargetSetRoot[:], targetSetRoot)
	copy(authorization.TrustedActorDigest[:], actorDigest)
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
		return transfer_service.LiveAuthorization{}, err
	}
	return authorization, nil
}

var _ transfer_service.LiveAuthorizationRepository = (*Repository)(nil)
