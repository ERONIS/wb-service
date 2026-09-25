package authorization

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
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
		logger:        core_logger.Fallback(loggers...),
	}
}

func (processor *AutomaticAuthorizationProcessor) ProcessPending(ctx context.Context) error {
	if ctx == nil {
		return errors.New("process automatic live authorization: context is nil")
	}
	if !processor.authorization.LiveEnabled() {
		return nil
	}
	processor.processMu.Lock()
	defer processor.processMu.Unlock()

	plans, err := processor.plans.ListAutomaticAuthorizationPlans(
		ctx,
		automaticAuthorizationPageSize,
	)
	if err != nil {
		return fmt.Errorf("list automatic live authorization plans: %w", err)
	}
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
) error {
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
	_, err := processor.authorization.Authorize(
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
