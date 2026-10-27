package monitoring

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type ErrorTargetSource interface {
	AllTargets(context.Context) ([]core_domain.CabinetAccess, error)
}

type ErrorFeedRepository interface {
	LoadErrorCursor(context.Context, core_domain.SellerID) (ErrorCursor, error)

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
