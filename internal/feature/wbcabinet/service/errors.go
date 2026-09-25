package wbcabinet_service

import (
	"context"
	"errors"
	"fmt"
	"net"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var (
	ErrInvalidCredential = fmt.Errorf("invalid WB credential: %w", core_errors.ErrInvalidArgument)
	ErrCredentialExpired = fmt.Errorf("WB credential expired: %w", core_errors.ErrInvalidArgument)
	ErrContentAccess     = fmt.Errorf("WB Content access is insufficient: %w", core_errors.ErrForbidden)
	ErrIdentityMismatch  = fmt.Errorf("WB cabinet identity mismatch: %w", core_errors.ErrConflict)
	ErrDuplicateSeller   = fmt.Errorf("WB seller is already registered: %w", core_errors.ErrConflict)
	ErrCabinetNameTaken  = fmt.Errorf("WB cabinet name is already registered: %w", core_errors.ErrConflict)
	// ErrVerification does not imply that WB rejected the credential: remote
	// verification may also fail because of a timeout or temporary outage.
	ErrVerification            = errors.New("WB credential verification failed")
	ErrVerificationRateLimited = errors.New("WB credential verification rate limited")
	ErrVerificationTransient   = errors.New("WB credential verification transient error")
)

func IsTransientVerificationError(err error) bool {
	if errors.Is(err, ErrVerificationTransient) ||
		errors.Is(err, ErrVerificationRateLimited) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
