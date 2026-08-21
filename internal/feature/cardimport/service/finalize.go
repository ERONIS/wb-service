package cardimport_service

import (
	"context"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func (s *Service) Finalize(
	ctx context.Context,
	actor TrustedActor,
	command FinalizeCommand,
) (BatchHeader, error) {
	actor = actor.normalized()
	command = command.normalized()
	if err := actor.validate(); err != nil {
		return BatchHeader{}, err
	}
	if err := command.validate(); err != nil {
		return BatchHeader{}, err
	}

	commandDigest := command.digest()
	var batch BatchHeader
	err := s.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			var err error
			batch, err = s.repository.Finalize(
				ctx,
				tx,
				actor,
				command,
				commandDigest,
			)
			if err != nil {
				return err
			}
			return nil
		},
	)
	if err != nil {
		return BatchHeader{}, fmt.Errorf("finalize cardimport session: %w", err)
	}

	return batch, nil
}

func (s *Service) GetBatch(
	ctx context.Context,
	batchID BatchID,
) (BatchHeader, error) {
	if batchID <= 0 {
		return BatchHeader{}, fmt.Errorf(
			"invalid cardimport batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.GetBatch(ctx, batchID)
}

func (s *Service) ListFinalizedBatches(
	ctx context.Context,
	after *BatchCursor,
	limit int,
) ([]BatchHeader, error) {
	if after != nil {
		if err := after.Validate(); err != nil {
			return nil, err
		}
	}
	if limit <= 0 || limit > MaxBatchPageSize {
		return nil, fmt.Errorf(
			"invalid finalized batch page limit '%d': %w",
			limit,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.ListFinalizedBatches(ctx, after, limit)
}

func (s *Service) ListBatchItems(
	ctx context.Context,
	batchID BatchID,
	afterPosition int,
	limit int,
) ([]BatchItem, error) {
	switch {
	case batchID <= 0:
		return nil, fmt.Errorf(
			"invalid cardimport batch ID '%d': %w",
			batchID,
			core_errors.ErrInvalidArgument,
		)
	case afterPosition < 0:
		return nil, fmt.Errorf(
			"invalid cardimport batch cursor '%d': %w",
			afterPosition,
			core_errors.ErrInvalidArgument,
		)
	case limit <= 0 || limit > MaxBatchPageSize:
		return nil, fmt.Errorf(
			"invalid cardimport batch page limit '%d': %w",
			limit,
			core_errors.ErrInvalidArgument,
		)
	}

	return s.repository.ListBatchItems(
		ctx,
		batchID,
		afterPosition,
		limit,
	)
}
