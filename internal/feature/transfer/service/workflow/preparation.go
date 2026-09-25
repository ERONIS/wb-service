package workflow

import (
	"context"
	"errors"
	"fmt"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

const MaxPreparingPageSize = 1000

func (service *Service) ListPreparing(
	ctx context.Context,
	afterID TransferID,
	limit int,
) ([]Transfer, error) {
	if ctx == nil {
		return nil, errors.New("list preparing transfers: context is nil")
	}
	if afterID < 0 || limit <= 0 || limit > MaxPreparingPageSize {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListPreparing(ctx, afterID, limit)
}

func (service *Service) BeginPreparation(
	ctx context.Context,
	transferID TransferID,
) error {
	if ctx == nil {
		return errors.New("begin transfer preparation: context is nil")
	}
	if transferID <= 0 {
		return core_errors.ErrInvalidArgument
	}
	if err := service.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return service.repository.BeginPreparation(ctx, tx, transferID)
		},
	); err != nil {
		return fmt.Errorf("begin transfer preparation: %w", err)
	}
	return nil
}

func (service *Service) LoadPreparationSource(
	ctx context.Context,
	transferID TransferID,
	groupTargetID int64,
) (PreparationSource, error) {
	if ctx == nil {
		return PreparationSource{}, errors.New(
			"load transfer preparation source: context is nil",
		)
	}
	if transferID <= 0 || groupTargetID <= 0 {
		return PreparationSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPreparationSource(
		ctx,
		transferID,
		groupTargetID,
	)
	if err != nil {
		return PreparationSource{}, err
	}
	if err := source.Validate(); err != nil {
		return PreparationSource{}, fmt.Errorf(
			"validate transfer preparation source: %w",
			err,
		)
	}
	return source, nil
}
