package cardprepare_service

import (
	"context"
	"errors"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type Coordinator struct {
	repository PreparationRepository
	uow        core_postgres_transaction.UnitOfWork
}

func NewCoordinator(
	repository PreparationRepository,
	uow core_postgres_transaction.UnitOfWork,
) *Coordinator {
	if repository == nil {
		panic("cardprepare repository is nil")
	}
	if uow == nil {
		panic("cardprepare unit of work is nil")
	}
	return &Coordinator{repository: repository, uow: uow}
}

func (coordinator *Coordinator) Start(
	ctx context.Context,
	command StartPreparationCommand,
) error {
	if ctx == nil {
		return errors.New("start card preparation: context is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	if err := coordinator.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return coordinator.repository.StartPreparation(ctx, tx, command)
		},
	); err != nil {
		return fmt.Errorf("start card preparation: %w", err)
	}
	return nil
}
