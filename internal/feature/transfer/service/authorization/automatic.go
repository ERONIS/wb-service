package authorization

import (
	"context"
	"errors"
	"fmt"
	"sync"

	core_logger "github.com/ERONIS/wb-service/internal/core/logger"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"

	"go.uber.org/zap"
)

const automaticAuthorizationPageSize = 100

type AutomaticAuthorizationPlanSource interface {
	ListAutomaticAuthorizationPlans(context.Context, transfer_model.TransferID, int64, int) ([]AuthorizationPlanSummary, error)
}

type AutomaticLiveAuthorization interface {
	LiveEnabled() bool
	Authorize(context.Context, LiveTrustedActor, RequestLiveCommand) (LiveAuthorization, error)
}

type AutomaticAuthorizationProcessor struct {
	plans           AutomaticAuthorizationPlanSource
	authorization   AutomaticLiveAuthorization
	logger          *zap.Logger
	processMu       sync.Mutex
	afterTransferID transfer_model.TransferID
	afterPlanID     int64
}

func NewAutomaticAuthorizationProcessor(
	plans AutomaticAuthorizationPlanSource,
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
	return processor.ProcessPendingWithNotify(ctx, nil)
}

func (processor *AutomaticAuthorizationProcessor) ProcessPendingWithNotify(
	ctx context.Context,
	onProgress func(),
) error {
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
		processor.afterTransferID,
		processor.afterPlanID,
		automaticAuthorizationPageSize,
	)
	if err != nil {
		return fmt.Errorf("list automatic live authorization plans: %w", err)
	}
	if len(plans) == 0 && processor.afterTransferID != 0 {
		processor.afterTransferID = 0
		processor.afterPlanID = 0
		plans, err = processor.plans.ListAutomaticAuthorizationPlans(
			ctx,
			0,
			0,
			automaticAuthorizationPageSize,
		)
		if err != nil {
			return fmt.Errorf("list automatic live authorization plans: %w", err)
		}
	}

	var firstErr error
	for _, plan := range plans {
		if err := processor.authorize(ctx, plan); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"authorize publication plan for transfer ID='%d': %w",
					plan.TransferID,
					err,
				)
			}
			continue
		}
		if onProgress != nil {
			onProgress()
		}
	}
	if len(plans) == automaticAuthorizationPageSize {
		last := plans[len(plans)-1]
		processor.afterTransferID = last.TransferID
		processor.afterPlanID = last.PlanID
	} else {
		processor.afterTransferID = 0
		processor.afterPlanID = 0
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
	keySuffix := fmt.Sprintf(
		"%d:%d:%d:%d",
		plan.TransferID,
		plan.PlanID,
		plan.PlanRevision,
		plan.LatestAuthorizationID,
	)
	_, err := processor.authorization.Authorize(
		ctx,
		actor,
		RequestLiveCommand{
			TransferID:     plan.TransferID,
			PlanID:         plan.PlanID,
			IdempotencyKey: "auto:authorize:" + keySuffix,
		},
	)
	return err
}
