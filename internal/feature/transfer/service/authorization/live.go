package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

const MaxLiveIdempotencyKeyLength = 160

var (
	ErrLiveModeDisabled = fmt.Errorf(
		"live transfer mode is disabled: %w",
		core_errors.ErrForbidden,
	)
	ErrLiveAuthorization = fmt.Errorf(
		"live authorization is invalid: %w",
		core_errors.ErrForbidden,
	)
	ErrLiveExpired = fmt.Errorf(
		"live authorization expired: %w",
		core_errors.ErrForbidden,
	)
	ErrLiveIdempotency = fmt.Errorf(
		"live authorization idempotency conflict: %w",
		core_errors.ErrConflict,
	)
	ErrLivePlanUnavailable = fmt.Errorf(
		"publication plan is unavailable for authorization: %w",
		core_errors.ErrNotFound,
	)
)

type LiveAuthorizationID int64

type LiveAuthorizationState string

const (
	LiveAuthorizationRequested  LiveAuthorizationState = "requested"
	LiveAuthorizationAuthorized LiveAuthorizationState = "authorized"
	LiveAuthorizationRevoked    LiveAuthorizationState = "revoked"
	LiveAuthorizationExpired    LiveAuthorizationState = "expired"
	LiveAuthorizationSuperseded LiveAuthorizationState = "superseded"
	LiveAuthorizationClosed     LiveAuthorizationState = "closed"
)

func (state LiveAuthorizationState) IsValid() bool {
	switch state {
	case LiveAuthorizationRequested,
		LiveAuthorizationAuthorized,
		LiveAuthorizationRevoked,
		LiveAuthorizationExpired,
		LiveAuthorizationSuperseded,
		LiveAuthorizationClosed:
		return true
	default:
		return false
	}
}

type LiveTrustedActor struct {
	TelegramUserID int64
	DisplayName    string
}

func (actor LiveTrustedActor) normalized() LiveTrustedActor {
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	return actor
}

func (actor LiveTrustedActor) validate() error {
	if actor.TelegramUserID <= 0 || actor.DisplayName == "" ||
		len([]rune(actor.DisplayName)) > 100 {
		return core_errors.InvalidArgument("trusted live actor is invalid")
	}
	return nil
}

type AuthorizationPlanSummary struct {
	TransferID            transfer_model.TransferID
	PlanID                int64
	PlanRevision          int64
	LatestAuthorizationID LiveAuthorizationID
	TargetsCount          int
	CreateActions         int
	AddActions            int
	MediaActions          int
	CreateItems           int
	AddItems              int
	ExistingItems         int
	ConflictItems         int
	AuthorTelegramID      int64
}

func (summary AuthorizationPlanSummary) Validate() error {
	if summary.TransferID <= 0 || summary.PlanID <= 0 ||
		summary.PlanRevision < 0 || summary.LatestAuthorizationID < 0 ||
		summary.TargetsCount <= 0 || summary.CreateActions < 0 ||
		summary.AddActions < 0 || summary.MediaActions < 0 ||
		summary.CreateItems < 0 || summary.AddItems < 0 ||
		summary.ExistingItems < 0 || summary.ConflictItems < 0 ||
		summary.AuthorTelegramID <= 0 {
		return errors.New("authorization plan summary is invalid")
	}
	return nil
}

func (summary AuthorizationPlanSummary) ActionsCount() int {
	return summary.CreateActions + summary.AddActions + summary.MediaActions
}

type LivePlanSource interface {
	ListAutomaticAuthorizationPlans(
		ctx context.Context,
		afterTransferID transfer_model.TransferID,
		afterPlanID int64,
		limit int,
	) ([]AuthorizationPlanSummary, error)

	ListAuthorizationPlans(
		ctx context.Context,
		actorTelegramID int64,
		limit int,
	) ([]AuthorizationPlanSummary, error)

	GetAuthorizationPlan(
		ctx context.Context,
		actorTelegramID int64,
		transferID transfer_model.TransferID,
	) (AuthorizationPlanSummary, error)

	LockAuthorizationPlan(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		actorTelegramID int64,
		transferID transfer_model.TransferID,
		planID int64,
	) (AuthorizationPlanSummary, error)
}

type LiveAuthorization struct {
	ID               LiveAuthorizationID
	TransferID       transfer_model.TransferID
	PlanID           int64
	Revision         int64
	State            LiveAuthorizationState
	TrustedActorID   int64
	TrustedActorName string
	RequestedAt      time.Time
	ApprovedAt       *time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	ClosedAt         *time.Time
	SafeReasonCode   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (authorization LiveAuthorization) Validate() error {
	if authorization.ID <= 0 || authorization.TransferID <= 0 ||
		authorization.PlanID <= 0 || authorization.Revision < 0 ||
		!authorization.State.IsValid() || authorization.TrustedActorID <= 0 ||
		strings.TrimSpace(authorization.TrustedActorName) == "" ||
		authorization.RequestedAt.IsZero() || authorization.ExpiresAt.IsZero() ||
		authorization.CreatedAt.IsZero() || authorization.UpdatedAt.IsZero() {
		return ErrLiveAuthorization
	}
	return nil
}

type RequestLiveCommand struct {
	TransferID     transfer_model.TransferID
	PlanID         int64
	IdempotencyKey string
}

func (command RequestLiveCommand) normalized() RequestLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command RequestLiveCommand) validate() error {
	if command.TransferID <= 0 || command.PlanID <= 0 ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("request live command is invalid")
	}
	return nil
}

type ApproveLiveCommand struct {
	TransferID       transfer_model.TransferID
	AuthorizationID  LiveAuthorizationID
	ExpectedRevision int64
	ExpectedPlanID   int64
	IdempotencyKey   string
}

func (command ApproveLiveCommand) normalized() ApproveLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command ApproveLiveCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanID <= 0 ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("approve live command is invalid")
	}
	return nil
}

type RevokeLiveCommand struct {
	TransferID       transfer_model.TransferID
	AuthorizationID  LiveAuthorizationID
	ExpectedRevision int64
	SafeReasonCode   string
	IdempotencyKey   string
}

type ChangeLiveAuthorizationStateCommand struct {
	TransferID       transfer_model.TransferID
	AuthorizationID  LiveAuthorizationID
	ExpectedRevision int64
	ExpectedPlanID   int64
	SafeReasonCode   string
}

func (command ChangeLiveAuthorizationStateCommand) normalized() ChangeLiveAuthorizationStateCommand {
	command.SafeReasonCode = strings.TrimSpace(command.SafeReasonCode)
	return command
}

func (command ChangeLiveAuthorizationStateCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanID <= 0 ||
		command.SafeReasonCode == "" || len(command.SafeReasonCode) > 128 {
		return core_errors.InvalidArgument("change live authorization state command is invalid")
	}
	return nil
}

func (command RevokeLiveCommand) normalized() RevokeLiveCommand {
	command.SafeReasonCode = strings.TrimSpace(command.SafeReasonCode)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command RevokeLiveCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.SafeReasonCode == "" ||
		len(command.SafeReasonCode) > 128 || command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return core_errors.InvalidArgument("revoke live command is invalid")
	}
	return nil
}

type LiveAuthorizationCheck struct {
	TransferID                    transfer_model.TransferID
	ActionID                      int64
	AuthorizationID               LiveAuthorizationID
	ExpectedAuthorizationRevision int64
	PlanID                        int64
	TargetID                      int64
	SellerID                      core_domain.SellerID
	ClientGeneration              core_domain.ClientGeneration
}

func (check LiveAuthorizationCheck) validate() error {
	if check.TransferID <= 0 || check.ActionID <= 0 || check.AuthorizationID <= 0 ||
		check.ExpectedAuthorizationRevision < 0 || check.PlanID <= 0 || check.TargetID <= 0 ||
		check.SellerID.Validate() != nil ||
		check.ClientGeneration.IsZero() {
		return ErrLiveAuthorization
	}
	return nil
}

type LiveAuthorizationEvidence struct {
	AuthorizationID  LiveAuthorizationID
	Revision         int64
	TransferID       transfer_model.TransferID
	ActionID         int64
	PlanID           int64
	TargetID         int64
	SellerID         core_domain.SellerID
	ClientGeneration core_domain.ClientGeneration
	ActorID          int64
	ApprovedAt       time.Time
	ExpiresAt        time.Time
}

type LiveAuthorizationRepository interface {
	RequestLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		expiresAt time.Time,
		actor LiveTrustedActor,
		command RequestLiveCommand,
		plan AuthorizationPlanSummary,
	) (LiveAuthorization, error)

	ApproveLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		actor LiveTrustedActor,
		command ApproveLiveCommand,
	) (LiveAuthorization, error)

	RevokeLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		actor LiveTrustedActor,
		command RevokeLiveCommand,
	) (LiveAuthorization, error)

	ChangeLiveState(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		state LiveAuthorizationState,
		command ChangeLiveAuthorizationStateCommand,
		idempotencyKey string,
	) (LiveAuthorization, error)

	LockValidLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		check LiveAuthorizationCheck,
	) (LiveAuthorizationEvidence, error)
}

type LiveAuthorizationService struct {
	repository LiveAuthorizationRepository
	plans      LivePlanSource
	uow        core_postgres_transaction.UnitOfWork
	live       bool
	ttl        time.Duration
	now        func() time.Time
}

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

func (service *LiveAuthorizationService) LockValid(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	check LiveAuthorizationCheck,
) (LiveAuthorizationEvidence, error) {
	if !service.live {
		return LiveAuthorizationEvidence{}, ErrLiveModeDisabled
	}
	if ctx == nil || tx == nil {
		return LiveAuthorizationEvidence{}, ErrLiveAuthorization
	}
	if err := check.validate(); err != nil {
		return LiveAuthorizationEvidence{}, err
	}
	return service.repository.LockValidLive(ctx, tx, service.now().UTC(), check)
}

func (service *LiveAuthorizationService) SupersedeWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	return service.changeStateWithin(ctx, tx, LiveAuthorizationSuperseded, command)
}

func (service *LiveAuthorizationService) CloseWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	return service.changeStateWithin(ctx, tx, LiveAuthorizationClosed, command)
}

func (service *LiveAuthorizationService) changeStateWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	state LiveAuthorizationState,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	command = command.normalized()
	if ctx == nil || tx == nil {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if state != LiveAuthorizationSuperseded && state != LiveAuthorizationClosed {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	authorization, err := service.repository.ChangeLiveState(
		ctx,
		tx,
		service.now().UTC(),
		state,
		command,
		fmt.Sprintf(
			"system:%s-live:%d:%d",
			state,
			command.AuthorizationID,
			command.ExpectedRevision,
		),
	)
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("change live authorization state: %w", err)
	}
	return authorization, nil
}
