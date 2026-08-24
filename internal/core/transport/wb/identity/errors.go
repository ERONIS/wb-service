package identity

import (
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var ErrIdentityMismatch = fmt.Errorf(
	"WB cabinet identity mismatch: %w",
	core_errors.ErrConflict,
)

var ErrDuplicateSeller = fmt.Errorf(
	"WB seller is already registered: %w",
	core_errors.ErrConflict,
)
