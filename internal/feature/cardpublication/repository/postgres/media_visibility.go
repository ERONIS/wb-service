package cardpublication_postgres_repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	tx "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_execution "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/execution"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func (repository *Repository) ScheduleMediaVisibility(ctx context.Context, dbtx tx.DBTX,
	action cardpublication_execution.MediaAction, attempt cardpublication_execution.MediaAttempt, result cardpublication_execution.MediaMutationResult,
	interval, timeout time.Duration,
) error {
	if (result.Disposition != cardpublication_execution.SubmissionAccepted && result.Disposition != cardpublication_execution.SubmissionUncertain) ||
		result.Validate() != nil ||
		interval <= 0 || timeout <= interval {
		return errors.New("schedule publication media visibility: invalid submission")
	}
	if err := recordMediaAttemptResult(ctx, dbtx, action, attempt, result); err != nil {
		return err
	}
	const advance = `
		UPDATE wb.publication_actions
		SET state = 'reconciling', revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE transfer_id = $1 AND id = $2 AND revision = $3 AND state = 'dispatching';
	`
	tag, err := dbtx.Exec(ctx, advance, action.TransferID, action.ActionID, action.Revision)
	if err != nil {
		return fmt.Errorf("advance media action to visibility: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return cardpublication_execution.ErrMediaActionConflict
	}
	const insert = `
		INSERT INTO wb.publication_media_visibility
			(transfer_id, action_id, attempt_id, next_check_at, deadline_at)
		VALUES ($1, $2, $3,
			CURRENT_TIMESTAMP + $4 * INTERVAL '1 microsecond',
			CURRENT_TIMESTAMP + $5 * INTERVAL '1 microsecond');
	`
	firstCheck := min(interval, 10*time.Second)
	if firstCheck <= 0 {
		firstCheck = interval
	}
	_, err = dbtx.Exec(ctx, insert, action.TransferID, action.ActionID, attempt.ID,
		firstCheck.Microseconds(), timeout.Microseconds())
	if err != nil {
		return fmt.Errorf("schedule media visibility: %w", err)
	}
	return nil
}

func (repository *Repository) ClaimMediaVisibility(ctx context.Context, limit int, lease time.Duration) ([]cardpublication_execution.MediaVisibilityJob, error) {
	if limit <= 0 || limit > 100 || lease <= 0 {
		return nil, errors.New("media visibility claim is invalid")
	}
	ctx, cancel := repository.pool.OperationContext(ctx)
	defer cancel()
	const query = `
		WITH due AS (
			SELECT job.transfer_id, job.action_id
			FROM wb.publication_media_visibility AS job
			WHERE job.finished_at IS NULL AND job.next_check_at <= CURRENT_TIMESTAMP
			  AND (job.leased_until IS NULL OR job.leased_until <= CURRENT_TIMESTAMP)
			ORDER BY job.next_check_at, job.action_id
			LIMIT $1
			FOR UPDATE OF job SKIP LOCKED
		), claimed AS (
			UPDATE wb.publication_media_visibility AS job
			SET leased_until = CURRENT_TIMESTAMP + $2 * INTERVAL '1 microsecond',
			    revision = job.revision + 1, check_count = job.check_count + 1
			FROM due
			WHERE job.transfer_id = due.transfer_id AND job.action_id = due.action_id
			RETURNING job.*
		)
		SELECT action.transfer_id, action.id, action.target_id, target.seller_id,
		       auth.id, auth.revision, plan.id,
		       job.attempt_id, job.revision AS claim_revision, job.deadline_at, job.check_count
		FROM claimed AS job
		JOIN wb.publication_actions AS action
		  ON action.transfer_id = job.transfer_id AND action.id = job.action_id
		JOIN wb.publication_plans AS plan
		  ON plan.transfer_id = action.transfer_id AND plan.id = action.plan_id
		JOIN wb.transfer_targets AS target
		  ON target.transfer_id = action.transfer_id AND target.id = action.target_id
		JOIN wb.transfer_live_authorizations AS auth
		  ON auth.transfer_id = action.transfer_id AND auth.id = action.authorization_id
		WHERE action.kind = 'upload_media' AND action.state = 'reconciling';
	`
	rows, err := repository.pool.Query(ctx, query, limit, lease.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("claim media visibility: %w", err)
	}
	defer rows.Close()
	jobs := make([]cardpublication_execution.MediaVisibilityJob, 0)
	for rows.Next() {
		var job cardpublication_execution.MediaVisibilityJob
		c := &job.Candidate
		if err := rows.Scan(&c.TransferID, &c.ActionID, &c.TargetID, &c.SellerID,
			&c.AuthorizationID, &c.AuthorizationRevision, &c.PlanID,
			&job.AttemptID, &job.Revision, &job.DeadlineAt, &job.CheckCount); err != nil {
			return nil, fmt.Errorf("scan media visibility: %w", err)
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (repository *Repository) UpdateMediaVisibility(ctx context.Context, dbtx tx.DBTX,
	action cardpublication_execution.MediaAction, job cardpublication_execution.MediaVisibilityJob, update cardpublication_execution.MediaVisibilityUpdate,
) (cardpublication_execution.MediaGroupResult, error) {
	if dbtx == nil || action.State != "reconciling" || action.AttemptID != job.AttemptID ||
		action.TransferID != job.Candidate.TransferID || action.ActionID != job.Candidate.ActionID ||
		job.Revision <= 0 || update.NextDelay <= 0 || update.Observation.PhotosCount < 0 || len(update.ErrorCode) > 128 {
		return cardpublication_execution.MediaGroupResult{}, cardpublication_execution.ErrMediaActionConflict
	}
	terminal := update.OutcomeClass != ""
	if (!terminal && update.OutcomeCode != "") || (terminal &&
		!((update.OutcomeClass == transfer.ResultSuccess && update.OutcomeCode == "WB_MEDIA_VISIBLE" && update.Observation.Visible) ||
			(update.OutcomeClass == transfer.ResultUnresolved && update.OutcomeCode == "WB_MEDIA_VISIBILITY_TIMEOUT" && !update.Observation.Visible))) {
		return cardpublication_execution.MediaGroupResult{}, errors.New("media visibility outcome is invalid")
	}
	const query = `
		UPDATE wb.publication_media_visibility AS job
		SET next_check_at = LEAST(deadline_at, CURRENT_TIMESTAMP + $5 * INTERVAL '1 microsecond'),
		    leased_until = NULL, revision = revision + 1,
		    last_card_found = $6, last_photos_count = $7, last_error_code = $8,
		    finished_at = CASE WHEN $9 THEN CURRENT_TIMESTAMP ELSE NULL END
		WHERE transfer_id = $1 AND action_id = $2 AND attempt_id = $3 AND revision = $4
		  AND finished_at IS NULL
		  AND (NOT $10 OR deadline_at <= clock_timestamp())
		  AND EXISTS (
			SELECT 1 FROM wb.publication_attempts AS attempt
			WHERE attempt.transfer_id = job.transfer_id AND attempt.id = job.attempt_id
			  AND attempt.action_id = job.action_id AND attempt.finished_at IS NOT NULL
			  AND attempt.delivery_state IN ('response_received', 'unknown_delivery')
			  AND attempt.response_disposition IN ('accepted', 'uncertain')
		  );
	`
	tag, err := dbtx.Exec(ctx, query, action.TransferID, action.ActionID, job.AttemptID, job.Revision,
		update.NextDelay.Microseconds(), update.Observation.CardFound, update.Observation.PhotosCount,
		update.ErrorCode, terminal, update.OutcomeClass == transfer.ResultUnresolved)
	if err != nil {
		return cardpublication_execution.MediaGroupResult{}, fmt.Errorf("update media visibility: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return cardpublication_execution.MediaGroupResult{}, cardpublication_execution.ErrMediaActionConflict
	}
	if !terminal {
		return cardpublication_execution.MediaGroupResult{GroupTargetID: action.Member.GroupTargetID}, nil
	}
	return finishMediaAction(ctx, dbtx, action, update.OutcomeClass, update.OutcomeCode)
}
