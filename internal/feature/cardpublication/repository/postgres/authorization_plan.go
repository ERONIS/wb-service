package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"

	"github.com/jackc/pgx/v5"
)

const authorizationPlanColumns = `
	transfer.id,
	plan.id,
	plan.plan_digest,
	plan.target_set_root,
	plan.revision,
	(
		SELECT COUNT(*)
		FROM wb.publication_actions AS action
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
		  AND action.kind = 'create_group'
		  AND action.state = 'planned'
	),
	(
		SELECT COUNT(*)
		FROM wb.publication_actions AS action
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
		  AND action.kind = 'add_to_group'
		  AND action.state = 'planned'
	),
	(
		SELECT COUNT(*)
		FROM wb.publication_actions AS action
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
		  AND action.kind = 'upload_media'
		  AND action.state = 'planned'
	),
	(
		SELECT COUNT(*)
		FROM wb.transfer_item_targets AS item_target
		WHERE item_target.transfer_id = transfer.id
		  AND item_target.state = 'terminal'
		  AND item_target.outcome_class = 'skipped'
		  AND item_target.outcome_code IN (
			'ALREADY_PRESENT_COMPATIBLE',
			'ALREADY_PRESENT_DIFFERENT_UNTOUCHED'
		  )
	),
	(
		SELECT COUNT(*)
		FROM wb.transfer_item_targets AS item_target
		WHERE item_target.transfer_id = transfer.id
		  AND item_target.state = 'terminal'
		  AND item_target.outcome_class = 'rejected'
	),
	session.author_telegram_id
`

func (repository *Repository) ListAuthorizationPlans(
	ctx context.Context,
	actorTelegramID int64,
	limit int,
) ([]transfer_service.AuthorizationPlanSummary, error) {
	if actorTelegramID <= 0 || limit <= 0 || limit > 100 {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	query := `
		SELECT ` + authorizationPlanColumns + `
		FROM wb.publication_plans AS plan
		JOIN wb.transfers AS transfer
		  ON transfer.id = plan.transfer_id
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		JOIN wb.card_import_sessions AS session
		  ON session.id = batch.source_session_id
		WHERE session.author_telegram_id = $1
			AND transfer.phase IN (
				'awaiting_authorization', 'publishing', 'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND plan.state IN ('awaiting_authorization', 'executing')
			AND EXISTS (
				SELECT 1
				FROM wb.publication_actions AS pending_action
				WHERE pending_action.transfer_id = plan.transfer_id
				  AND pending_action.plan_id = plan.id
				  AND pending_action.state = 'planned'
			)
			AND NOT EXISTS (
				SELECT 1
				FROM wb.transfer_live_authorizations AS authorization
				WHERE authorization.transfer_id = transfer.id
				  AND authorization.plan_digest = plan.plan_digest
				  AND authorization.state = 'authorized'
				  AND authorization.expires_at > CURRENT_TIMESTAMP
			)
		ORDER BY transfer.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, actorTelegramID, limit)
	if err != nil {
		return nil, fmt.Errorf("list authorization plans: %w", err)
	}
	defer rows.Close()
	plans := make([]transfer_service.AuthorizationPlanSummary, 0)
	for rows.Next() {
		plan, err := scanAuthorizationPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan authorization plan: %w", err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorization plans: %w", err)
	}
	return plans, nil
}

func (repository *Repository) GetAuthorizationPlan(
	ctx context.Context,
	actorTelegramID int64,
	transferID transfer_service.TransferID,
) (transfer_service.AuthorizationPlanSummary, error) {
	if actorTelegramID <= 0 || transferID <= 0 {
		return transfer_service.AuthorizationPlanSummary{}, core_errors.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, repository.pool.OpTimeout())
	defer cancel()
	return loadAuthorizationPlan(
		ctx,
		repository.pool,
		actorTelegramID,
		transferID,
		transfer_service.Digest{},
		false,
	)
}

func (repository *Repository) LockAuthorizationPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	actorTelegramID int64,
	transferID transfer_service.TransferID,
	planDigest transfer_service.Digest,
) (transfer_service.AuthorizationPlanSummary, error) {
	if tx == nil || actorTelegramID <= 0 || transferID <= 0 ||
		planDigest == (transfer_service.Digest{}) {
		return transfer_service.AuthorizationPlanSummary{}, core_errors.ErrInvalidArgument
	}
	return loadAuthorizationPlan(
		ctx,
		tx,
		actorTelegramID,
		transferID,
		planDigest,
		true,
	)
}

func loadAuthorizationPlan(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	actorTelegramID int64,
	transferID transfer_service.TransferID,
	expectedDigest transfer_service.Digest,
	lock bool,
) (transfer_service.AuthorizationPlanSummary, error) {
	query := `
		SELECT ` + authorizationPlanColumns + `
		FROM wb.publication_plans AS plan
		JOIN wb.transfers AS transfer
		  ON transfer.id = plan.transfer_id
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		JOIN wb.card_import_sessions AS session
		  ON session.id = batch.source_session_id
		WHERE session.author_telegram_id = $1
			AND transfer.id = $2
			AND transfer.phase IN (
				'awaiting_authorization', 'publishing', 'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND plan.state IN ('awaiting_authorization', 'executing')
			AND EXISTS (
				SELECT 1
				FROM wb.publication_actions AS pending_action
				WHERE pending_action.transfer_id = plan.transfer_id
				  AND pending_action.plan_id = plan.id
				  AND pending_action.state = 'planned'
			)
	`
	arguments := []any{actorTelegramID, transferID}
	if expectedDigest != (transfer_service.Digest{}) {
		query += "\t\t\tAND plan.plan_digest = $3\n"
		arguments = append(arguments, expectedDigest[:])
	}
	if lock {
		query += "\t\tFOR UPDATE OF plan;\n"
	} else {
		query += "\t\tORDER BY plan.id DESC LIMIT 1;\n"
	}
	plan, err := scanAuthorizationPlan(db.QueryRow(ctx, query, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_service.AuthorizationPlanSummary{}, transfer_service.ErrLivePlanUnavailable
	}
	if err != nil {
		return transfer_service.AuthorizationPlanSummary{}, fmt.Errorf(
			"load authorization plan: %w",
			err,
		)
	}
	return plan, nil
}

func scanAuthorizationPlan(row interface{ Scan(...any) error }) (
	transfer_service.AuthorizationPlanSummary,
	error,
) {
	var (
		plan                      transfer_service.AuthorizationPlanSummary
		transferID                int64
		planDigest, targetSetRoot []byte
	)
	if err := row.Scan(
		&transferID,
		&plan.PlanID,
		&planDigest,
		&targetSetRoot,
		&plan.PlanRevision,
		&plan.CreateActions,
		&plan.AddActions,
		&plan.MediaActions,
		&plan.ExistingItems,
		&plan.ConflictItems,
		&plan.AuthorTelegramID,
	); err != nil {
		return transfer_service.AuthorizationPlanSummary{}, err
	}
	if len(planDigest) != len(plan.PlanDigest) ||
		len(targetSetRoot) != len(plan.TargetSetRoot) {
		return transfer_service.AuthorizationPlanSummary{}, errors.New(
			"authorization plan digest has invalid length",
		)
	}
	plan.TransferID = transfer_service.TransferID(transferID)
	copy(plan.PlanDigest[:], planDigest)
	copy(plan.TargetSetRoot[:], targetSetRoot)
	if err := plan.Validate(); err != nil {
		return transfer_service.AuthorizationPlanSummary{}, err
	}
	return plan, nil
}

var _ transfer_service.LivePlanSource = (*Repository)(nil)
