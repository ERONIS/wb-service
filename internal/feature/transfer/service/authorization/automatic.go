package authorization

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	core_observability "github.com/ERONIS/wb-service/internal/core/observability"
	"go.uber.org/zap"
)

const automaticAuthorizationPageSize = 100

type AutomaticLiveAuthorization interface {
	LiveEnabled() bool
	Authorize(context.Context, LiveTrustedActor, RequestLiveCommand) (LiveAuthorization, error)
}

type AutomaticAuthorizationProcessor struct {
	plans         LivePlanSource
	authorization AutomaticLiveAuthorization
	logger        *zap.Logger
	processMu     sync.Mutex
}

func NewAutomaticAuthorizationProcessor(
	plans LivePlanSource,
	authorization AutomaticLiveAuthorization,
	loggers ...*zap.Logger,
) *AutomaticAuthorizationProcessor {
	if plans == nil || authorization == nil {
		panic("automatic live authorization dependency is nil")
	}
	return &AutomaticAuthorizationProcessor{
		plans:         plans,
		authorization: authorization,
		logger:        core_observability.Logger(loggers...),
	}
}

func (processor *AutomaticAuthorizationProcessor) ProcessPending(ctx context.Context) (err error) {
	startedAt := time.Now()
	plansCount := 0
	var lockWait time.Duration
	defer func() {
		logTiming := core_observability.LogTimingDebug
		if plansCount > 0 {
			logTiming = core_observability.LogTiming
		}
		logTiming(
			processor.logger,
			"transfer",
			"automatic_authorization_poll",
			startedAt,
			err,
			zap.Duration("lock_wait_duration", lockWait),
			zap.Int("plans_count", plansCount),
		)
	}()
	if ctx == nil {
		return errors.New("process automatic live authorization: context is nil")
	}
	if !processor.authorization.LiveEnabled() {
		return nil
	}
	lockStartedAt := time.Now()
	processor.processMu.Lock()
	lockWait = time.Since(lockStartedAt)
	defer processor.processMu.Unlock()

	plans, err := processor.plans.ListAutomaticAuthorizationPlans(
		ctx,
		automaticAuthorizationPageSize,
	)
	if err != nil {
		return fmt.Errorf("list automatic live authorization plans: %w", err)
	}
	plansCount = len(plans)
	var firstErr error
	for _, plan := range plans {
		if err := processor.authorize(ctx, plan); err != nil && firstErr == nil {
			firstErr = fmt.Errorf(
				"authorize publication plan for transfer ID='%d': %w",
				plan.TransferID,
				err,
			)
		}
	}
	return firstErr
}

func (processor *AutomaticAuthorizationProcessor) authorize(
	ctx context.Context,
	plan AuthorizationPlanSummary,
) (err error) {
	startedAt := time.Now()
	defer func() {
		core_observability.LogTiming(
			processor.logger,
			"transfer",
			"automatic_authorization",
			startedAt,
			err,
			zap.Int64("transfer_id", int64(plan.TransferID)),
			zap.Int64("plan_revision", plan.PlanRevision),
		)
	}()
	if err := plan.Validate(); err != nil {
		return err
	}
	actor := LiveTrustedActor{
		TelegramUserID: plan.AuthorTelegramID,
		DisplayName:    fmt.Sprintf("Telegram %d", plan.AuthorTelegramID),
	}
	digest := hex.EncodeToString(plan.PlanDigest[:])
	keySuffix := fmt.Sprintf(
		"%d:%d:%d:%s",
		plan.TransferID,
		plan.PlanRevision,
		plan.LatestAuthorizationID,
		digest,
	)
	_, err = processor.authorization.Authorize(
		ctx,
		actor,
		RequestLiveCommand{
			TransferID:     plan.TransferID,
			PlanDigest:     plan.PlanDigest,
			IdempotencyKey: "auto:authorize:" + keySuffix,
		},
	)
	return err
}
