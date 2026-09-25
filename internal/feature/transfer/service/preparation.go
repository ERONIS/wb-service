package transfer_service

import (
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
)

type PreparationSourceItem = preparation.PreparationSourceItem
type PreparationSource = preparation.PreparationSource
type PreparationResultStatus = preparation.PreparationResultStatus
type ApplyPreparationResultCommand = preparation.ApplyPreparationResultCommand
type PreparationResultRepository = preparation.PreparationResultRepository
type PreparationResultApplier = preparation.PreparationResultApplier

const (
	PreparationResultSucceeded  = preparation.PreparationResultSucceeded
	PreparationResultRejected   = preparation.PreparationResultRejected
	PreparationResultUnresolved = preparation.PreparationResultUnresolved
)

var ErrPreparationResultConflict = preparation.ErrPreparationResultConflict

func NewPreparationResultApplier(
	repository PreparationResultRepository,
	uow core_postgres_transaction.UnitOfWork,
) *PreparationResultApplier {
	return preparation.NewPreparationResultApplier(repository, uow)
}
