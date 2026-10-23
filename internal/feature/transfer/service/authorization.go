package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"

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
