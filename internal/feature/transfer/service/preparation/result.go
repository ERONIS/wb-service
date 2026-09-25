package preparation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

var ErrPreparationResultConflict = fmt.Errorf(
	"transfer preparation result conflicts with projection: %w",
	core_errors.ErrConflict,
)

type PreparationResultStatus string

const (
	PreparationResultSucceeded  PreparationResultStatus = "succeeded"
	PreparationResultRejected   PreparationResultStatus = "rejected"
	PreparationResultUnresolved PreparationResultStatus = "unresolved"
)

type ApplyPreparationResultCommand struct {
	TransferID         TransferID
	GroupTargetID      int64
	PreparationID      int64
	PreparationGroupID int64
	Status             PreparationResultStatus
	OutcomeCode        string
	ProposalRoot       Digest
	Revision           int64
}

func (command ApplyPreparationResultCommand) Validate() error {
	if command.TransferID <= 0 || command.GroupTargetID <= 0 ||
		command.PreparationID <= 0 || command.PreparationGroupID <= 0 ||
		command.Revision != 1 || strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		command.OutcomeCode == "" || len(command.OutcomeCode) > 128 {
		return errors.New("apply transfer preparation result command is invalid")
	}
	switch command.Status {
	case PreparationResultSucceeded:
		if command.OutcomeCode != "prepared" || command.ProposalRoot == (Digest{}) {
			return errors.New("successful transfer preparation result is invalid")
		}
	case PreparationResultRejected, PreparationResultUnresolved:
		if command.ProposalRoot != (Digest{}) {
			return errors.New("unsuccessful transfer preparation result has proposal root")
		}
	default:
		return errors.New("transfer preparation result status is invalid")
	}
	return nil
}

type PreparationResultApplier struct {
	repository PreparationResultRepository
	uow        core_postgres_transaction.UnitOfWork
}

func NewPreparationResultApplier(
	repository PreparationResultRepository,
	uow core_postgres_transaction.UnitOfWork,
) *PreparationResultApplier {
	if repository == nil {
		panic("transfer preparation result repository is nil")
	}
	if uow == nil {
		panic("transfer preparation result unit of work is nil")
	}
	return &PreparationResultApplier{
		repository: repository,
		uow:        uow,
	}
}

func (applier *PreparationResultApplier) Apply(
	ctx context.Context,
	command ApplyPreparationResultCommand,
) error {
	if ctx == nil {
		return errors.New("apply transfer preparation result: context is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	if err := applier.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return applier.repository.ApplyPreparationResult(ctx, tx, command)
		},
	); err != nil {
		return fmt.Errorf("apply transfer preparation result: %w", err)
	}
	return nil
}
