package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"sort"

	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

// ListDispatchableProductActions возвращает список действий с товарами, готовых к диспетчеризации.
func (repository *Repository) ListDispatchableProductActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.ProductActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("dispatchable product action limit is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		WITH dispatchable AS (
		SELECT
			action.transfer_id AS transfer_id,
			action.id AS action_id,
			action.target_id AS target_id,
			target.cabinet_id AS cabinet_id,
			live_auth.id AS authorization_id,
			live_auth.revision AS authorization_revision,
			plan.plan_digest,
			plan.target_set_root,
			target.position AS target_position,
			ROW_NUMBER() OVER (
				PARTITION BY target.cabinet_id
				ORDER BY transfer.id, action.id
			) AS cabinet_rank,
			ARRAY(
				SELECT member.vendor_code
				FROM wb.publication_action_members AS member
				WHERE member.transfer_id = action.transfer_id
					AND member.action_id = action.id
				ORDER BY member.request_member_index
			) AS catalog_vendor_codes
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfers AS transfer
		  ON transfer.id = action.transfer_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.plan_id = action.plan_id
		 AND live_auth.plan_digest = plan.plan_digest
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'planned'
			AND action.authorization_id IS NULL
			AND plan.state IN ('awaiting_authorization', 'executing')
			AND transfer.phase IN (
				'awaiting_authorization', 'publishing', 'reconciling', 'media'
			)
			AND transfer.outcome = 'running'
			AND live_auth.state = 'authorized'
			AND live_auth.expires_at > CURRENT_TIMESTAMP
			AND NOT EXISTS (
				SELECT 1
				FROM wb.publication_attempts AS attempt
				WHERE attempt.action_id = action.id
			)
		)
		SELECT
			transfer_id,
			action_id,
			target_id,
			cabinet_id,
			authorization_id,
			authorization_revision,
			plan_digest,
			target_set_root,
			catalog_vendor_codes
		FROM dispatchable
		ORDER BY cabinet_rank, target_position, cabinet_id, action_id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list dispatchable product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanCatalogProductActionCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dispatchable product actions: %w", err)
	}
	return result, nil
}

// ListInterruptedProductActions возвращает прерванные действия товаров (находящиеся в dispatching без ответа).
func (repository *Repository) ListInterruptedProductActions(
	ctx context.Context,
	limit int,
) ([]cardpublication_service.ProductActionCandidate, error) {
	if limit <= 0 || limit > 100 {
		return nil, errors.New("interrupted product action limit is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		SELECT
			action.transfer_id,
			action.id,
			action.target_id,
			target.cabinet_id,
			live_auth.id,
			live_auth.revision,
			plan.plan_digest,
			plan.target_set_root
		FROM wb.publication_actions AS action
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id
		 AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id
		 AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS live_auth
		  ON live_auth.transfer_id = action.transfer_id
		 AND live_auth.id = action.authorization_id
		JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		WHERE action.kind IN ('create_group', 'add_to_group')
			AND action.state = 'dispatching'
			AND attempt.delivery_state = 'not_dispatched'
			AND attempt.finished_at IS NULL
		ORDER BY action.id
		LIMIT $1;
	`
	rows, err := repository.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list interrupted product actions: %w", err)
	}
	defer rows.Close()
	result := make([]cardpublication_service.ProductActionCandidate, 0)
	for rows.Next() {
		candidate, err := scanProductActionCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted product actions: %w", err)
	}
	return result, nil
}

// scanProductActionCandidate сканирует строку в ProductActionCandidate.
func scanProductActionCandidate(row interface{ Scan(...any) error }) (
	cardpublication_service.ProductActionCandidate,
	error,
) {
	var candidate cardpublication_service.ProductActionCandidate
	var transferID, authorizationID int64
	var planDigest, targetSetRoot []byte
	if err := row.Scan(
		&transferID,
		&candidate.ActionID,
		&candidate.TargetID,
		&candidate.CabinetID,
		&authorizationID,
		&candidate.AuthorizationRevision,
		&planDigest,
		&targetSetRoot,
	); err != nil {
		return cardpublication_service.ProductActionCandidate{}, err
	}
	if len(planDigest) != len(candidate.PlanDigest) ||
		len(targetSetRoot) != len(candidate.TargetSetRoot) {
		return cardpublication_service.ProductActionCandidate{},
			errors.New("publication product candidate digest is invalid")
	}
	candidate.TransferID = transfer_service.TransferID(transferID)
	candidate.AuthorizationID = transfer_service.LiveAuthorizationID(authorizationID)
	copy(candidate.PlanDigest[:], planDigest)
	copy(candidate.TargetSetRoot[:], targetSetRoot)
	return candidate, nil
}

// scanCatalogProductActionCandidate сканирует строку в ProductActionCandidate с кодами вендоров.
func scanCatalogProductActionCandidate(row interface{ Scan(...any) error }) (
	cardpublication_service.ProductActionCandidate,
	error,
) {
	var candidate cardpublication_service.ProductActionCandidate
	var transferID, authorizationID int64
	var planDigest, targetSetRoot []byte
	if err := row.Scan(
		&transferID,
		&candidate.ActionID,
		&candidate.TargetID,
		&candidate.CabinetID,
		&authorizationID,
		&candidate.AuthorizationRevision,
		&planDigest,
		&targetSetRoot,
		&candidate.CatalogVendorCodes,
	); err != nil {
		return cardpublication_service.ProductActionCandidate{}, err
	}
	if len(planDigest) != len(candidate.PlanDigest) ||
		len(targetSetRoot) != len(candidate.TargetSetRoot) ||
		len(candidate.CatalogVendorCodes) == 0 {
		return cardpublication_service.ProductActionCandidate{},
			errors.New("catalog publication product candidate is invalid")
	}
	candidate.TransferID = transfer_service.TransferID(transferID)
	candidate.AuthorizationID = transfer_service.LiveAuthorizationID(authorizationID)
	copy(candidate.PlanDigest[:], planDigest)
	copy(candidate.TargetSetRoot[:], targetSetRoot)
	return candidate, nil
}

// uniqueSortedStrings возвращает дедуплицированный отсортированный срез строк.
func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
