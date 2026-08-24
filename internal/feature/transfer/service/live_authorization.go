package transfer_service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
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
		return fmt.Errorf("trusted live actor is invalid: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func (actor LiveTrustedActor) digest() Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-live-actor:v1")
	writeInt64(hasher, actor.TelegramUserID)
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

type AuthorizationPlanSummary struct {
	TransferID       TransferID
	PlanID           int64
	PlanDigest       Digest
	TargetSetRoot    Digest
	PlanRevision     int64
	CreateActions    int
	AddActions       int
	MediaActions     int
	ExistingItems    int
	ConflictItems    int
	AuthorTelegramID int64
}

func (summary AuthorizationPlanSummary) Validate() error {
	if summary.TransferID <= 0 || summary.PlanID <= 0 ||
		summary.PlanDigest == (Digest{}) || summary.TargetSetRoot == (Digest{}) ||
		summary.PlanRevision < 0 || summary.CreateActions < 0 ||
		summary.AddActions < 0 || summary.MediaActions < 0 ||
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
	ListAuthorizationPlans(
		ctx context.Context,
		actorTelegramID int64,
		limit int,
	) ([]AuthorizationPlanSummary, error)

	GetAuthorizationPlan(
		ctx context.Context,
		actorTelegramID int64,
		transferID TransferID,
	) (AuthorizationPlanSummary, error)

	LockAuthorizationPlan(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		actorTelegramID int64,
		transferID TransferID,
		planDigest Digest,
	) (AuthorizationPlanSummary, error)
}

type LiveAuthorization struct {
	ID                 LiveAuthorizationID
	TransferID         TransferID
	PlanID             int64
	PlanDigest         Digest
	TargetSetRoot      Digest
	Revision           int64
	State              LiveAuthorizationState
	TrustedActorID     int64
	TrustedActorDigest Digest
	TrustedActorName   string
	RequestedAt        time.Time
	ApprovedAt         *time.Time
	ExpiresAt          time.Time
	RevokedAt          *time.Time
	ClosedAt           *time.Time
	SafeReasonCode     string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (authorization LiveAuthorization) Validate() error {
	if authorization.ID <= 0 || authorization.TransferID <= 0 ||
		authorization.PlanID <= 0 || authorization.PlanDigest == (Digest{}) ||
		authorization.TargetSetRoot == (Digest{}) || authorization.Revision < 0 ||
		!authorization.State.IsValid() || authorization.TrustedActorID <= 0 ||
		authorization.TrustedActorDigest == (Digest{}) ||
		strings.TrimSpace(authorization.TrustedActorName) == "" ||
		authorization.RequestedAt.IsZero() || authorization.ExpiresAt.IsZero() ||
		authorization.CreatedAt.IsZero() || authorization.UpdatedAt.IsZero() {
		return ErrLiveAuthorization
	}
	return nil
}

type RequestLiveCommand struct {
	TransferID     TransferID
	PlanDigest     Digest
	IdempotencyKey string
}

func (command RequestLiveCommand) normalized() RequestLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command RequestLiveCommand) validate() error {
	if command.TransferID <= 0 || command.PlanDigest == (Digest{}) ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return fmt.Errorf("request live command is invalid: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func (command RequestLiveCommand) digest() Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-request-live:v1")
	writeInt64(hasher, int64(command.TransferID))
	writeBytes(hasher, command.PlanDigest[:])
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

type ApproveLiveCommand struct {
	TransferID         TransferID
	AuthorizationID    LiveAuthorizationID
	ExpectedRevision   int64
	ExpectedPlanDigest Digest
	IdempotencyKey     string
}

func (command ApproveLiveCommand) normalized() ApproveLiveCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	return command
}

func (command ApproveLiveCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanDigest == (Digest{}) ||
		command.IdempotencyKey == "" ||
		len(command.IdempotencyKey) > MaxLiveIdempotencyKeyLength {
		return fmt.Errorf("approve live command is invalid: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func (command ApproveLiveCommand) digest() Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-approve-live:v1")
	writeInt64(hasher, int64(command.TransferID))
	writeInt64(hasher, int64(command.AuthorizationID))
	writeInt64(hasher, command.ExpectedRevision)
	writeBytes(hasher, command.ExpectedPlanDigest[:])
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

type RevokeLiveCommand struct {
	TransferID       TransferID
	AuthorizationID  LiveAuthorizationID
	ExpectedRevision int64
	SafeReasonCode   string
	IdempotencyKey   string
}

type ChangeLiveAuthorizationStateCommand struct {
	TransferID         TransferID
	AuthorizationID    LiveAuthorizationID
	ExpectedRevision   int64
	ExpectedPlanDigest Digest
	SafeReasonCode     string
}

func (command ChangeLiveAuthorizationStateCommand) normalized() ChangeLiveAuthorizationStateCommand {
	command.SafeReasonCode = strings.TrimSpace(command.SafeReasonCode)
	return command
}

func (command ChangeLiveAuthorizationStateCommand) validate() error {
	if command.TransferID <= 0 || command.AuthorizationID <= 0 ||
		command.ExpectedRevision < 0 || command.ExpectedPlanDigest == (Digest{}) ||
		command.SafeReasonCode == "" || len(command.SafeReasonCode) > 128 {
		return fmt.Errorf("change live authorization state command is invalid: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func (command ChangeLiveAuthorizationStateCommand) digest(state LiveAuthorizationState) Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-change-live-state:v1")
	writeString(hasher, string(state))
	writeInt64(hasher, int64(command.TransferID))
	writeInt64(hasher, int64(command.AuthorizationID))
	writeInt64(hasher, command.ExpectedRevision)
	writeBytes(hasher, command.ExpectedPlanDigest[:])
	writeString(hasher, command.SafeReasonCode)
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
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
		return fmt.Errorf("revoke live command is invalid: %w", core_errors.ErrInvalidArgument)
	}
	return nil
}

func (command RevokeLiveCommand) digest() Digest {
	hasher := sha256.New()
	writeString(hasher, "transfer-revoke-live:v1")
	writeInt64(hasher, int64(command.TransferID))
	writeInt64(hasher, int64(command.AuthorizationID))
	writeInt64(hasher, command.ExpectedRevision)
	writeString(hasher, command.SafeReasonCode)
	var result Digest
	copy(result[:], hasher.Sum(nil))
	return result
}

type LiveAuthorizationCheck struct {
	TransferID                    TransferID
	ActionID                      int64
	AuthorizationID               LiveAuthorizationID
	ExpectedAuthorizationRevision int64
	PlanDigest                    Digest
	TargetSetRoot                 Digest
	TargetID                      int64
	CabinetID                     CabinetID
	SellerKey                     Digest
	ClientGeneration              ClientGeneration
}

func (check LiveAuthorizationCheck) validate() error {
	if check.TransferID <= 0 || check.ActionID <= 0 || check.AuthorizationID <= 0 ||
		check.ExpectedAuthorizationRevision < 0 || check.PlanDigest == (Digest{}) ||
		check.TargetSetRoot == (Digest{}) || check.TargetID <= 0 ||
		check.CabinetID == "" || check.SellerKey == (Digest{}) ||
		check.ClientGeneration == (ClientGeneration{}) {
		return ErrLiveAuthorization
	}
	return nil
}

type LiveAuthorizationEvidence struct {
	AuthorizationID  LiveAuthorizationID
	Revision         int64
	TransferID       TransferID
	ActionID         int64
	PlanDigest       Digest
	TargetSetRoot    Digest
	TargetID         int64
	CabinetID        CabinetID
	SellerKey        Digest
	ClientGeneration ClientGeneration
	ActorDigest      Digest
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
		actorDigest Digest,
		command RequestLiveCommand,
		commandDigest Digest,
		plan AuthorizationPlanSummary,
	) (LiveAuthorization, error)

	ApproveLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		actor LiveTrustedActor,
		actorDigest Digest,
		command ApproveLiveCommand,
		commandDigest Digest,
	) (LiveAuthorization, error)

	RevokeLive(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		actor LiveTrustedActor,
		actorDigest Digest,
		command RevokeLiveCommand,
		commandDigest Digest,
	) (LiveAuthorization, error)

	ChangeLiveState(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		now time.Time,
		state LiveAuthorizationState,
		command ChangeLiveAuthorizationStateCommand,
		actorDigest Digest,
		commandDigest Digest,
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
			command.PlanDigest,
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
			actor.digest(),
			command,
			command.digest(),
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
			command.ExpectedPlanDigest,
		); err != nil {
			return err
		}
		var err error
		authorization, err = service.repository.ApproveLive(
			ctx,
			tx,
			now,
			actor,
			actor.digest(),
			command,
			command.digest(),
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
			actor.digest(),
			command,
			command.digest(),
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

// SupersedeWithin invalidates an exact authorization in the caller's
// transaction when its immutable plan is replaced before dispatch begins.
func (service *LiveAuthorizationService) SupersedeWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	return service.changeStateWithin(ctx, tx, LiveAuthorizationSuperseded, command)
}

// CloseWithin closes an exact authorization in the caller's transaction after
// every action covered by its plan has reached a terminal result.
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
	actorDigest := systemLiveActorDigest()
	authorization, err := service.repository.ChangeLiveState(
		ctx,
		tx,
		service.now().UTC(),
		state,
		command,
		actorDigest,
		command.digest(state),
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

func systemLiveActorDigest() Digest {
	value := sha256.Sum256([]byte("transfer-system-actor:v1"))
	var result Digest
	copy(result[:], value[:])
	return result
}
