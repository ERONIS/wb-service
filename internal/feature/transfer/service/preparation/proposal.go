package preparation

import (
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

var (
	ErrPreparationMismatch = core_errors.ErrConflict
	ErrProposalNotFound    = core_errors.ErrNotFound
)
