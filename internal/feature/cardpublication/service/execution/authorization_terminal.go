package execution

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type terminalPlanRepository interface {
	PublicationPlanTerminal(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_service.TransferID,
		int64,
	) (bool, error)
}

func closeTerminalPlanAuthorization(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	repository terminalPlanRepository,
	authorization LiveAuthorizationVerifier,
	candidate ProductActionCandidate,
	planID int64,
) error {
	terminal, err := repository.PublicationPlanTerminal(ctx, tx, candidate.TransferID, planID)
	if err != nil || !terminal {
		return err
	}
	_, err = authorization.CloseWithin(
		ctx,
		tx,
		transfer_service.ChangeLiveAuthorizationStateCommand{
			TransferID:         candidate.TransferID,
			AuthorizationID:    candidate.AuthorizationID,
			ExpectedRevision:   candidate.AuthorizationRevision,
			ExpectedPlanDigest: transfer_service.Digest(candidate.PlanDigest),
			SafeReasonCode:     "publication_plan_terminal",
		},
	)
	return err
}
