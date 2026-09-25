package workflow

import (
	"context"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

// ListRecoverableFiles returns durable intermediate files so infrastructure
// can resume them without keeping a Telegram callback open.
func (s *Service) ListRecoverableFiles(
	ctx context.Context,
	staleParsingBefore time.Time,
	reparsePriceErrorsBefore time.Time,
	limit int,
) ([]RecoverableFile, error) {
	if ctx == nil || staleParsingBefore.IsZero() ||
		reparsePriceErrorsBefore.IsZero() || limit <= 0 {
		return nil, fmt.Errorf(
			"invalid recoverable cardimport files query: %w",
			core_errors.ErrInvalidArgument,
		)
	}

	files, err := s.repository.ListRecoverableFiles(
		ctx,
		staleParsingBefore,
		reparsePriceErrorsBefore,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recoverable cardimport files: %w", err)
	}
	for _, file := range files {
		if file.AuthorTelegramID <= 0 {
			return nil, fmt.Errorf(
				"recoverable cardimport file has invalid owner: %w",
				core_errors.ErrInvalidArgument,
			)
		}
		if err := file.File.Validate(); err != nil {
			return nil, fmt.Errorf("validate recoverable cardimport file: %w", err)
		}
	}

	return files, nil
}
