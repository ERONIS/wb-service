package publication

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type PublicationPlanResultRepository interface {
	ApplyPublicationPlanResult(
		context.Context,
		core_postgres_transaction.DBTX,
		ApplyPublicationPlanResultCommand,
	) error
}
