package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	tx "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
)

const mediaVisibilityRequestTimeout = 45 * time.Second
const mediaVisibilityLease = 2 * time.Minute

type MediaVisibilityJob struct {
	Candidate  MediaActionCandidate
	AttemptID  int64
	Revision   int64
	DeadlineAt time.Time
	CheckCount int
}

type MediaVisibilityUpdate struct {
	Observation  catalog.MediaVisibility
	NextDelay    time.Duration
	ErrorCode    string
	OutcomeClass transfer.ResultClass
	OutcomeCode  string
}

// ProcessMediaVisibility is polled separately from sending requests. Each claim
// performs at most one observation; sleeping and retries are represented in DB.
func (dispatcher *MediaDispatcher) ProcessMediaVisibility(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process media visibility: context is nil")
	}
	dispatcher.visibilityMu.Lock()
	defer dispatcher.visibilityMu.Unlock()
	jobs, err := dispatcher.repository.ClaimMediaVisibility(ctx, min(dispatcher.concurrency, 5), mediaVisibilityLease)
	if err != nil {
		return err
	}
	errs := make([]error, len(jobs))
	var workers sync.WaitGroup
	for i, job := range jobs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errs[i] = dispatcher.checkMediaVisibility(ctx, job)
		}()
	}
	workers.Wait()
	return errors.Join(errs...)
}

func (dispatcher *MediaDispatcher) checkMediaVisibility(ctx context.Context, job MediaVisibilityJob) (err error) {
	startedAt := time.Now()
	ctx = core_observability.WithCorrelation(ctx, core_observability.Correlation{
		TransferID: int64(job.Candidate.TransferID), ActionID: job.Candidate.ActionID,
	})
	update := MediaVisibilityUpdate{NextDelay: mediaVisibilityDelay(dispatcher.mediaCheckInterval, job.CheckCount)}
	defer func() {
		core_observability.LogTiming(core_observability.LoggerWithContext(ctx, dispatcher.logger),
			"cardpublication", "check_media_visibility", startedAt, err,
			zap.Int("check_count", job.CheckCount), zap.Time("deadline_at", job.DeadlineAt),
			zap.Bool("card_found", update.Observation.CardFound), zap.Int("photos_count", update.Observation.PhotosCount),
			zap.String("outcome_code", update.OutcomeCode), zap.String("check_error_code", update.ErrorCode))
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	action, err := dispatcher.loadLockedAction(ctx, job.Candidate)
	if err != nil {
		return err
	}
	if action.State != "reconciling" || action.AttemptID != job.AttemptID {
		return ErrMediaActionConflict
	}
	request, _, _, err := action.FinalRequest()
	if err != nil {
		return err
	}
	if time.Now().Before(job.DeadlineAt) {
		checkCtx, cancel := context.WithDeadline(ctx, minTime(job.DeadlineAt, time.Now().Add(mediaVisibilityRequestTimeout)))
		update.Observation, err = dispatcher.catalogReader.CheckMediaVisibility(checkCtx, action.CabinetID,
			action.Member.VendorCode, action.NMID, len(request.Data), false)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		} // A restart never turns a pending check into a terminal failure.
		if err != nil {
			update.ErrorCode = "WB_MEDIA_VISIBILITY_CHECK_FAILED"
		}
	}
	if update.Observation.Visible {
		update.OutcomeClass, update.OutcomeCode = transfer.ResultSuccess, "WB_MEDIA_VISIBLE"
	} else if !time.Now().Before(job.DeadlineAt) {
		update.OutcomeClass, update.OutcomeCode = transfer.ResultUnresolved, "WB_MEDIA_VISIBILITY_TIMEOUT"
	}
	resultCtx, cancel := publicationResultContext(ctx)
	defer cancel()
	return dispatcher.uow.WithinTransaction(resultCtx, func(ctx context.Context, dbtx tx.DBTX) error {
		locked, err := dispatcher.repository.LockMediaAction(ctx, dbtx, job.Candidate)
		if err != nil {
			return err
		}
		if locked.State != "reconciling" || locked.AttemptID != job.AttemptID {
			return ErrMediaActionConflict
		}
		group, err := dispatcher.repository.UpdateMediaVisibility(ctx, dbtx, locked, job, update)
		if err != nil {
			return fmt.Errorf("persist media visibility check: %w", err)
		}
		if err := dispatcher.applyGroupResult(ctx, dbtx, locked.TransferID, group); err != nil {
			return err
		}
		if update.OutcomeClass == "" {
			return nil
		}
		return dispatcher.closeAuthorizationIfPlanTerminal(ctx, dbtx, locked)
	})
}

func mediaVisibilityDelay(base time.Duration, _ int) time.Duration {
	return max(base, time.Minute)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
