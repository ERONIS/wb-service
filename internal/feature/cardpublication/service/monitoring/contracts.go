package monitoring

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type ErrorTargetSource interface {
	PublicationTargets(context.Context) ([]transfer_service.MutationTarget, error)
}

type ErrorFeedRepository interface {
	LoadErrorCursor(context.Context, CabinetID) (ErrorCursor, error)

	SaveErrorFeed(
		context.Context,
		core_postgres_transaction.DBTX,
		SaveErrorFeedCommand,
	) (ErrorCursor, error)

	CaptureErrorBaseline(
		context.Context,
		core_postgres_transaction.DBTX,
		CaptureErrorBaselineCommand,
	) (ErrorBaseline, error)
}
