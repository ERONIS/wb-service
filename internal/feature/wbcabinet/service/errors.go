package wbcabinet_service

import (
	"errors"
	"net"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	credentials "github.com/ERONIS/wb-service/internal/core/transport/wb/credentials"
)

var (
	ErrInvalidCredential = credentials.ErrInvalidCredential
	ErrCredentialExpired = credentials.ErrCredentialExpired
	ErrContentAccess     = core_errors.ErrForbidden
	ErrIdentityMismatch  = core_errors.ErrConflict
	ErrDuplicateSeller   = core_errors.ErrConflict
	ErrCabinetNameTaken  = core_errors.ErrConflict
	// ErrVerification не доказывает отклонение токена: проверка может завершиться
	// ошибкой из-за таймаута или временной недоступности WB.
	ErrVerification            = core_errors.ErrUnauthorized
	ErrVerificationRateLimited = core_errors.ErrRateLimited
	ErrVerificationTransient   = core_errors.ErrUnavailable
)

func IsTransientVerificationError(err error) bool {
	if core_errors.IsUnavailable(err) ||
		core_errors.IsRateLimited(err) ||
		core_errors.IsTimeout(err) ||
		core_errors.IsCanceled(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
