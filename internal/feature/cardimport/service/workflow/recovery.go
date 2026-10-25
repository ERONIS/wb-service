package cardimport_workflow

import (
	"context"
	"fmt"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
)

// ListRecoverableFiles находит файлы, обработку которых можно повторить.
func (s *Service) ListRecoverableFiles(
	ctx context.Context,
	staleParsingBefore time.Time,
	reparsePriceErrorsBefore time.Time,
	limit int,
) ([]cardimport_contract.RecoverableFile, error) {
	if ctx == nil || staleParsingBefore.IsZero() ||
		reparsePriceErrorsBefore.IsZero() || limit <= 0 {
		return nil, fmt.Errorf(
			"invalid recoverable cardimport files query: %w",
			core_errors.ErrInvalidArgument,
		)
	}

	files, err := s.sessions.Recoverable(
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
