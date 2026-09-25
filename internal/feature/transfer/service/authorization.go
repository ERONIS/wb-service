package transfer_service

import (
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	"go.uber.org/zap"
)

type LiveAuthorizationID = authorization.LiveAuthorizationID
type LiveAuthorizationState = authorization.LiveAuthorizationState
type LiveTrustedActor = authorization.LiveTrustedActor
type AuthorizationPlanSummary = authorization.AuthorizationPlanSummary
type LivePlanSource = authorization.LivePlanSource
type LiveAuthorization = authorization.LiveAuthorization
type RequestLiveCommand = authorization.RequestLiveCommand
type ApproveLiveCommand = authorization.ApproveLiveCommand
type RevokeLiveCommand = authorization.RevokeLiveCommand
type ChangeLiveAuthorizationStateCommand = authorization.ChangeLiveAuthorizationStateCommand
type LiveAuthorizationCheck = authorization.LiveAuthorizationCheck
type LiveAuthorizationEvidence = authorization.LiveAuthorizationEvidence
type LiveAuthorizationRepository = authorization.LiveAuthorizationRepository
type LiveAuthorizationService = authorization.LiveAuthorizationService
type AutomaticLiveAuthorization = authorization.AutomaticLiveAuthorization
type AutomaticAuthorizationProcessor = authorization.AutomaticAuthorizationProcessor

const (
	MaxLiveIdempotencyKeyLength = authorization.MaxLiveIdempotencyKeyLength

	LiveAuthorizationRequested  = authorization.LiveAuthorizationRequested
	LiveAuthorizationAuthorized = authorization.LiveAuthorizationAuthorized
	LiveAuthorizationRevoked    = authorization.LiveAuthorizationRevoked
	LiveAuthorizationExpired    = authorization.LiveAuthorizationExpired
	LiveAuthorizationSuperseded = authorization.LiveAuthorizationSuperseded
	LiveAuthorizationClosed     = authorization.LiveAuthorizationClosed
)

var (
	ErrLiveModeDisabled    = authorization.ErrLiveModeDisabled
	ErrLiveAuthorization   = authorization.ErrLiveAuthorization
	ErrLiveExpired         = authorization.ErrLiveExpired
	ErrLiveIdempotency     = authorization.ErrLiveIdempotency
	ErrLivePlanUnavailable = authorization.ErrLivePlanUnavailable
)

func NewLiveAuthorizationService(
	repository LiveAuthorizationRepository,
	plans LivePlanSource,
	uow core_postgres_transaction.UnitOfWork,
	live bool,
	ttl time.Duration,
) *LiveAuthorizationService {
	return authorization.NewLiveAuthorizationService(repository, plans, uow, live, ttl)
}

func NewAutomaticAuthorizationProcessor(
	plans LivePlanSource,
	authorizationService AutomaticLiveAuthorization,
	loggers ...*zap.Logger,
) *AutomaticAuthorizationProcessor {
	return authorization.NewAutomaticAuthorizationProcessor(plans, authorizationService, loggers...)
}
