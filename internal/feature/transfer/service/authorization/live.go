package authorization

import (
	"context"
	"fmt"
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

type LiveTrustedActor struct {
	TelegramUserID int64
	DisplayName    string
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

type RequestLiveCommand struct {
	TransferID     transfer_model.TransferID
	PlanID         int64
	IdempotencyKey string
}

type ApproveLiveCommand struct {
	TransferID       transfer_model.TransferID
	AuthorizationID  LiveAuthorizationID
	ExpectedRevision int64
	ExpectedPlanID   int64
	IdempotencyKey   string
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

	LockValidProductRetry(
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
