package wbcabinet_service

import (
	"errors"
	"net"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var (
	ErrInvalidCredential       = core_errors.ErrInvalidArgument
	ErrCredentialExpired       = core_errors.ErrInvalidArgument
	ErrContentAccess           = core_errors.ErrForbidden
	ErrIdentityMismatch        = core_errors.ErrConflict
	ErrDuplicateSeller         = core_errors.ErrConflict
	ErrCabinetNameTaken        = core_errors.ErrConflict
	// ErrVerification does not imply that WB rejected the credential: remote
	// verification may also fail because of a timeout or temporary outage.
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
