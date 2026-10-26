package authorization

import (
	"context"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func NewLiveAuthorizationService(
	repository LiveAuthorizationRepository,
	plans LivePlanSource,
	uow core_postgres_transaction.UnitOfWork,
	live bool,
	ttl time.Duration,
) *LiveAuthorizationService {
	if repository == nil || plans == nil || uow == nil || ttl <= 0 {
		panic("live authorization dependency is invalid")
	}
	return &LiveAuthorizationService{
		repository: repository,
		plans:      plans,
		uow:        uow,
		live:       live,
		ttl:        ttl,
		now:        time.Now,
	}
}

func (service *LiveAuthorizationService) LiveEnabled() bool {
	return service.live
}

func (service *LiveAuthorizationService) Request(
	ctx context.Context,
	actor LiveTrustedActor,
	command RequestLiveCommand,
) (LiveAuthorization, error) {
	if !service.live {
		return LiveAuthorization{}, ErrLiveModeDisabled
	}
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	now := service.now().UTC()
	var authorization LiveAuthorization
	err := service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		plan, err := service.plans.LockAuthorizationPlan(
			ctx,
			tx,
			actor.TelegramUserID,
			command.TransferID,
			command.PlanID,
		)
		if err != nil {
			return err
		}
		authorization, err = service.repository.RequestLive(
			ctx,
			tx,
			now,
			now.Add(service.ttl),
			actor,
			command,
			plan,
		)
		return err
	})
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("request live authorization: %w", err)
	}
	if authorization.State == LiveAuthorizationExpired {
		return LiveAuthorization{}, ErrLiveExpired
	}
	return authorization, nil
}

func (service *LiveAuthorizationService) Authorize(
	ctx context.Context,
	actor LiveTrustedActor,
	command RequestLiveCommand,
) (LiveAuthorization, error) {
	if !service.live {
		return LiveAuthorization{}, ErrLiveModeDisabled
	}
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	approveKey := command.IdempotencyKey + ":approve"
	if len(approveKey) > MaxLiveIdempotencyKeyLength {
		return LiveAuthorization{}, fmt.Errorf(
			"automatic approve idempotency key is too long: %w",
			core_errors.ErrInvalidArgument,
		)
	}
	now := service.now().UTC()
	var authorization LiveAuthorization
	err := service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		plan, err := service.plans.LockAuthorizationPlan(
			ctx,
			tx,
			actor.TelegramUserID,
			command.TransferID,
			command.PlanID,
		)
		if err != nil {
			return err
		}
		authorization, err = service.repository.RequestLive(
			ctx,
			tx,
			now,
			now.Add(service.ttl),
			actor,
			command,
			plan,
		)
		if err != nil || authorization.State == LiveAuthorizationAuthorized {
			return err
		}
		approve := ApproveLiveCommand{
			TransferID:       command.TransferID,
			AuthorizationID:  authorization.ID,
			ExpectedRevision: authorization.Revision,
			ExpectedPlanID:   command.PlanID,
			IdempotencyKey:   approveKey,
		}
		authorization, err = service.repository.ApproveLive(
			ctx,
			tx,
			now,
			actor,
			approve,
		)
		return err
	})
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("authorize live publication: %w", err)
	}
	if authorization.State == LiveAuthorizationExpired {
		return LiveAuthorization{}, ErrLiveExpired
	}
	if authorization.State != LiveAuthorizationAuthorized {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	return authorization, nil
}

func (service *LiveAuthorizationService) Approve(
	ctx context.Context,
	actor LiveTrustedActor,
	command ApproveLiveCommand,
) (LiveAuthorization, error) {
	if !service.live {
		return LiveAuthorization{}, ErrLiveModeDisabled
	}
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	now := service.now().UTC()
	var authorization LiveAuthorization
	err := service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		if _, err := service.plans.LockAuthorizationPlan(
			ctx,
			tx,
			actor.TelegramUserID,
			command.TransferID,
			command.ExpectedPlanID,
		); err != nil {
			return err
		}
		var err error
		authorization, err = service.repository.ApproveLive(
			ctx,
			tx,
			now,
			actor,
			command,
		)
		return err
	})
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("approve live authorization: %w", err)
	}
	if authorization.State == LiveAuthorizationExpired {
		return LiveAuthorization{}, ErrLiveExpired
	}
	if authorization.State != LiveAuthorizationAuthorized {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	return authorization, nil
}

func (service *LiveAuthorizationService) Revoke(
	ctx context.Context,
	actor LiveTrustedActor,
	command RevokeLiveCommand,
) (LiveAuthorization, error) {
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	var authorization LiveAuthorization
	err := service.uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var err error
		authorization, err = service.repository.RevokeLive(
			ctx,
			tx,
			service.now().UTC(),
			actor,
			command,
		)
		return err
	})
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("revoke live authorization: %w", err)
	}
	if authorization.State == LiveAuthorizationExpired {
		return LiveAuthorization{}, ErrLiveExpired
	}
	return authorization, nil
}
