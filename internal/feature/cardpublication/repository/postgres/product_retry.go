package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_service "github.com/ERONIS/wb-service/internal/feature/cardpublication/service"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"

	"github.com/jackc/pgx/v5"
)

// BeginProductRetry создаёт запись о повторной попытке отправки при пустом/невалидном ответе WB после задержки.
func (repository *Repository) BeginProductRetry(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_execution.BeginProductRetryCommand,
) (cardpublication_execution.ProductRetry, error) {
	action := command.Action
	if tx == nil || action.State != "reconciling" || action.AttemptID <= 0 ||
		action.EmptyResponseRetry.ID != 0 || action.AuthorizationID <= 0 {
		return cardpublication_execution.ProductRetry{}, errors.New(
			"begin publication product retry command is invalid",
		)
	}
	const insert = `
		INSERT INTO wb.publication_product_retries (
			transfer_id, action_id, authorization_id, request_payload
		)
		SELECT action.transfer_id, action.id, action.authorization_id,
		       action.request_payload
		FROM wb.publication_actions AS action
		JOIN wb.publication_attempts AS attempt
		  ON attempt.transfer_id = action.transfer_id
		 AND attempt.action_id = action.id
		WHERE action.transfer_id = $1
		  AND action.id = $2
		  AND action.authorization_id = $3
		  AND action.state = 'reconciling'
		  AND attempt.id = $4
		  AND attempt.finished_at IS NOT NULL
		  AND attempt.finished_at <= CURRENT_TIMESTAMP - INTERVAL '10 minutes'
		  AND attempt.delivery_state = 'response_received'
		  AND attempt.http_status = 200
		  AND attempt.response_disposition = 'uncertain'
		  AND attempt.safe_error_code IN (
		      'WB_TRANSPORT_EMPTY_RESPONSE',
		      'WB_TRANSPORT_INVALID_RESPONSE'
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM wb.publication_product_retries AS existing
		      WHERE existing.transfer_id = action.transfer_id
		        AND existing.action_id = action.id
		  )
		RETURNING id, started_at;
	`
	retry := cardpublication_execution.ProductRetry{
		TransferID:      action.TransferID,
		ActionID:        action.ActionID,
		AuthorizationID: action.AuthorizationID,
		RequestPayload:  append([]byte(nil), action.RequestPayload...),
	}
	if err := tx.QueryRow(
		ctx,
		insert,
		action.TransferID,
		action.ActionID,
		action.AuthorizationID,
		action.AttemptID,
	).Scan(&retry.ID, &retry.StartedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardpublication_execution.ProductRetry{}, cardpublication_service.ErrProductActionConflict
		}
		return cardpublication_execution.ProductRetry{}, fmt.Errorf(
			"insert publication product retry: %w",
			err,
		)
	}
	retry.StartedAt = retry.StartedAt.UTC()
	const touchAction = `
		UPDATE wb.publication_actions
		SET revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
		  AND id = $2
		  AND state = 'reconciling';
	`
	updated, err := tx.Exec(ctx, touchAction, action.TransferID, action.ActionID)
	if err != nil {
		return cardpublication_execution.ProductRetry{}, fmt.Errorf(
			"touch publication action for product retry: %w",
			err,
		)
	}
	if updated.RowsAffected() != 1 {
		return cardpublication_execution.ProductRetry{}, cardpublication_service.ErrProductActionConflict
	}
	return retry, nil
}

// RecordProductRetry фиксирует результат повторной отправки пакета карточек в WB.
func (repository *Repository) RecordProductRetry(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command cardpublication_execution.RecordProductRetryCommand,
) error {
	if tx == nil {
		return errors.New("record publication product retry: DBTX is nil")
	}
	if err := cardpublication_service.ValidateSubmissionResult(command.Result); err != nil {
		return err
	}
	action, retry, result := command.Action, command.Retry, command.Result
	if action.State != "reconciling" || retry.ID <= 0 ||
		retry.TransferID != action.TransferID || retry.ActionID != action.ActionID ||
		retry.AuthorizationID != action.AuthorizationID ||
		!equalBytes(retry.RequestPayload, action.RequestPayload) || !retry.FinishedAt.IsZero() {
		return cardpublication_service.ErrProductActionConflict
	}
	if result.Disposition == cardpublication_service.SubmissionRejectedProven {
		members := make(map[int64]struct{}, len(action.Members))
		for _, member := range action.Members {
			members[member.ID] = struct{}{}
		}
		if len(result.MemberResults) != len(members) {
			return cardpublication_service.ErrProductActionConflict
		}
		for _, memberResult := range result.MemberResults {
			if _, exists := members[memberResult.ActionMemberID]; !exists {
				return cardpublication_service.ErrProductActionConflict
			}
			delete(members, memberResult.ActionMemberID)
		}
		if len(members) != 0 {
			return cardpublication_service.ErrProductActionConflict
		}
	}
	var httpStatus any
	if result.HTTPStatus > 0 {
		httpStatus = result.HTTPStatus
	}
	const finish = `
		UPDATE wb.publication_product_retries
		SET delivery_state = $5,
		    http_status = $6,
		    response_disposition = $7,
		    classifier_version = $8,
		    safe_error_code = $9,
		    unmatched_count = $10,
		    finished_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
		  AND id = $2
		  AND action_id = $3
		  AND authorization_id = $4
		  AND request_payload = $11
		  AND finished_at IS NULL;
	`
	updated, err := tx.Exec(
		ctx,
		finish,
		action.TransferID,
		retry.ID,
		action.ActionID,
		action.AuthorizationID,
		result.Delivery,
		httpStatus,
		result.Disposition,
		result.ClassifierVersion,
		result.SafeCode,
		result.UnmatchedCount,
		action.RequestPayload,
	)
	if err != nil {
		return fmt.Errorf("finish publication product retry: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return cardpublication_service.ErrProductActionConflict
	}
	const touchAction = `
		UPDATE wb.publication_actions
		SET revision = revision + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1
		  AND id = $2
		  AND state = 'reconciling';
	`
	updated, err = tx.Exec(ctx, touchAction, action.TransferID, action.ActionID)
	if err != nil {
		return fmt.Errorf("touch publication action after product retry: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return cardpublication_service.ErrProductActionConflict
	}
	return nil
}
