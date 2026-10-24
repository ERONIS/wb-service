package monitoring

import (
	"context"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type ErrorTargetSource interface {
	PublicationTargets(context.Context) ([]transfer_model.MutationTarget, error)
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
