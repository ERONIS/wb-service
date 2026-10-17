package credentials

import (
	"fmt"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var (
	ErrInvalidCredential = fmt.Errorf("invalid WB token: %w", core_errors.ErrInvalidArgument)
	ErrCredentialExpired = fmt.Errorf("WB token expired: %w", core_errors.ErrInvalidArgument)
)
