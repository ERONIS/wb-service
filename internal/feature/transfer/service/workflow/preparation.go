package workflow

import (
	"context"
	"errors"
	"fmt"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

const MaxPreparingPageSize = 1000

func (service *Service) ListPreparing(
	ctx context.Context,
	afterID transfer_model.TransferID,
	limit int,
) ([]transfer_model.Transfer, error) {
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
	transferID transfer_model.TransferID,
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

// ListPendingPreparationGroups возвращает порцию целевых групп переноса, ожидающих подготовки.
func (service *Service) ListPendingPreparationGroups(
	ctx context.Context,
	transferID transfer_model.TransferID,
	limit int,
	after transfer_preparation.PreparationPendingGroup,
) ([]transfer_preparation.PreparationPendingGroup, error) {
	if ctx == nil {
		return nil, errors.New("list pending preparation groups: context is nil")
	}
	if transferID <= 0 || limit <= 0 || limit > MaxPreparingPageSize || after.SourceGroupID < 0 || after.TargetID < 0 {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListPendingPreparationGroups(ctx, transferID, limit, after)
}

func (service *Service) LoadPreparationSource(
	ctx context.Context,
	transferID transfer_model.TransferID,
	groupTargetID int64,
) (transfer_preparation.PreparationSource, error) {
	if ctx == nil {
		return transfer_preparation.PreparationSource{}, errors.New(
			"load transfer preparation source: context is nil",
		)
	}
	if transferID <= 0 || groupTargetID <= 0 {
		return transfer_preparation.PreparationSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPreparationSource(
		ctx,
		transferID,
		groupTargetID,
	)
	if err != nil {
		return transfer_preparation.PreparationSource{}, err
	}
	if err := source.Validate(); err != nil {
		return transfer_preparation.PreparationSource{}, fmt.Errorf(
			"validate transfer preparation source: %w",
			err,
		)
	}
	return source, nil
}
