package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"github.com/jackc/pgx/v5"
)

const authorizationPlanColumns = `
	transfer.id,
	plan.id,
	plan.revision,
	COALESCE((
		SELECT MAX(previous_auth.id)
		FROM wb.transfer_live_authorizations AS previous_auth
		WHERE previous_auth.transfer_id = plan.transfer_id
		  AND previous_auth.plan_id = plan.id
	), 0),
	(
		SELECT COUNT(DISTINCT action.target_id)
		FROM wb.publication_actions AS action
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
	),
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
		FROM wb.publication_action_members AS member
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = member.transfer_id
		 AND action.id = member.action_id
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
		  AND action.kind = 'create_group'
	),
	(
		SELECT COUNT(*)
		FROM wb.publication_action_members AS member
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = member.transfer_id
		 AND action.id = member.action_id
		WHERE action.transfer_id = plan.transfer_id
		  AND action.plan_id = plan.id
		  AND action.kind = 'add_to_group'
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
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = item_target.transfer_id
		 AND group_target.id = item_target.group_target_id
		WHERE item_target.transfer_id = transfer.id
		  AND group_target.publication_plan_id = plan.id
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
		JOIN wb.transfer_group_targets AS group_target
		  ON group_target.transfer_id = item_target.transfer_id
		 AND group_target.id = item_target.group_target_id
		WHERE item_target.transfer_id = transfer.id
		  AND group_target.publication_plan_id = plan.id
		  AND item_target.state = 'terminal'
		  AND item_target.outcome_class = 'rejected'
	),
	batch.author_telegram_id
`

func (repository *Repository) ListAutomaticAuthorizationPlans(
	ctx context.Context,
	afterTransferID transfer_model.TransferID,
	afterPlanID int64,
	limit int,
) ([]transfer_authorization.AuthorizationPlanSummary, error) {
	if limit <= 0 || limit > 100 || afterTransferID < 0 || afterPlanID < 0 ||
		(afterTransferID == 0) != (afterPlanID == 0) {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	query := `
		SELECT ` + authorizationPlanColumns + `
		FROM wb.publication_plans AS plan
		JOIN wb.transfers AS transfer
		  ON transfer.id = plan.transfer_id
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		WHERE batch.sealed
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND (transfer.id, plan.id) > ($1::bigint, $2::bigint)
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
				FROM wb.transfer_live_authorizations AS live_auth
				WHERE live_auth.transfer_id = transfer.id
				  AND live_auth.plan_id = plan.id
				  AND live_auth.state = 'authorized'
				  AND live_auth.expires_at > CURRENT_TIMESTAMP
			)
		ORDER BY transfer.id, plan.id
		LIMIT $3;
	`
	rows, err := repository.pool.Query(ctx, query, afterTransferID, afterPlanID, limit)
	if err != nil {
		return nil, fmt.Errorf("list automatic authorization plans: %w", err)
	}
	defer rows.Close()
	plans := make([]transfer_authorization.AuthorizationPlanSummary, 0)
	for rows.Next() {
		plan, err := scanAuthorizationPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan automatic authorization plan: %w", err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate automatic authorization plans: %w", err)
	}
	return plans, nil
}

func (repository *Repository) ListAuthorizationPlans(
	ctx context.Context,
	actorTelegramID int64,
	limit int,
) ([]transfer_authorization.AuthorizationPlanSummary, error) {
	if actorTelegramID <= 0 || limit <= 0 || limit > 100 {
		return nil, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	query := `
		SELECT ` + authorizationPlanColumns + `
		FROM wb.publication_plans AS plan
		JOIN wb.transfers AS transfer
		  ON transfer.id = plan.transfer_id
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		WHERE batch.author_telegram_id = $1
			AND batch.sealed
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
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
				FROM wb.transfer_live_authorizations AS live_auth
				WHERE live_auth.transfer_id = transfer.id
				  AND live_auth.plan_id = plan.id
				  AND live_auth.state = 'authorized'
				  AND live_auth.expires_at > CURRENT_TIMESTAMP
			)
		ORDER BY transfer.id, plan.id
		LIMIT $2;
	`
	rows, err := repository.pool.Query(ctx, query, actorTelegramID, limit)
	if err != nil {
		return nil, fmt.Errorf("list authorization plans: %w", err)
	}
	defer rows.Close()
	plans := make([]transfer_authorization.AuthorizationPlanSummary, 0)
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
	transferID transfer_model.TransferID,
) (transfer_authorization.AuthorizationPlanSummary, error) {
	if actorTelegramID <= 0 || transferID <= 0 {
		return transfer_authorization.AuthorizationPlanSummary{}, core_errors.ErrInvalidArgument
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	return loadAuthorizationPlan(
		ctx,
		repository.pool,
		actorTelegramID,
		transferID,
		0,
		false,
	)
}

func (repository *Repository) LockAuthorizationPlan(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	actorTelegramID int64,
	transferID transfer_model.TransferID,
	planID int64,
) (transfer_authorization.AuthorizationPlanSummary, error) {
	if tx == nil || actorTelegramID <= 0 || transferID <= 0 ||
		planID <= 0 {
		return transfer_authorization.AuthorizationPlanSummary{}, core_errors.ErrInvalidArgument
	}
	return loadAuthorizationPlan(
		ctx,
		tx,
		actorTelegramID,
		transferID,
		planID,
		true,
	)
}

func loadAuthorizationPlan(
	ctx context.Context,
	db core_postgres_transaction.DBTX,
	actorTelegramID int64,
	transferID transfer_model.TransferID,
	expectedPlanID int64,
	lock bool,
) (transfer_authorization.AuthorizationPlanSummary, error) {
	query := `
		SELECT ` + authorizationPlanColumns + `
		FROM wb.publication_plans AS plan
		JOIN wb.transfers AS transfer
		  ON transfer.id = plan.transfer_id
		JOIN wb.card_batches AS batch
		  ON batch.id = transfer.batch_id
		WHERE batch.author_telegram_id = $1
			AND batch.sealed
			AND transfer.id = $2
			AND transfer.phase IN (
				'preparing', 'awaiting_authorization', 'publishing',
				'reconciling', 'media'
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
	if expectedPlanID > 0 {
		query += "\t\t\tAND plan.id = $3\n"
		arguments = append(arguments, expectedPlanID)
	}
	if lock {
		query += "\t\tFOR UPDATE OF plan;\n"
	} else {
		query += "\t\tORDER BY plan.id DESC LIMIT 1;\n"
	}
	plan, err := scanAuthorizationPlan(db.QueryRow(ctx, query, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer_authorization.AuthorizationPlanSummary{}, transfer_service.ErrLivePlanUnavailable
	}
	if err != nil {
		return transfer_authorization.AuthorizationPlanSummary{}, fmt.Errorf(
			"load authorization plan: %w",
			err,
		)
	}
	return plan, nil
}

func scanAuthorizationPlan(row interface{ Scan(...any) error }) (
	transfer_authorization.AuthorizationPlanSummary,
	error,
) {
	var (
		plan       transfer_authorization.AuthorizationPlanSummary
		transferID int64
	)
	if err := row.Scan(
		&transferID,
		&plan.PlanID,
		&plan.PlanRevision,
		&plan.LatestAuthorizationID,
		&plan.TargetsCount,
		&plan.CreateActions,
		&plan.CreateItems,
		&plan.AddItems,
		&plan.AddActions,
		&plan.MediaActions,
		&plan.ExistingItems,
		&plan.ConflictItems,
		&plan.AuthorTelegramID,
	); err != nil {
		return transfer_authorization.AuthorizationPlanSummary{}, err
	}
	plan.TransferID = transfer_model.TransferID(transferID)
	if err := plan.Validate(); err != nil {
		return transfer_authorization.AuthorizationPlanSummary{}, err
	}
	return plan, nil
}

var _ transfer_authorization.LivePlanSource = (*Repository)(nil)
