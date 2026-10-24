package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"

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
