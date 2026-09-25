package preparation

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type PreparationResultRepository interface {
	ApplyPreparationResult(
		context.Context,
		core_postgres_transaction.DBTX,
		ApplyPreparationResultCommand,
	) error
}
