package execution

import (
	"context"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type terminalPlanRepository interface {
	PublicationPlanTerminal(
		context.Context,
		core_postgres_transaction.DBTX,
		transfer_model.TransferID,
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
		transfer_authorization.ChangeLiveAuthorizationStateCommand{
			TransferID:       candidate.TransferID,
			AuthorizationID:  candidate.AuthorizationID,
			ExpectedRevision: candidate.AuthorizationRevision,
			ExpectedPlanID:   planID,
			SafeReasonCode:   "publication_plan_terminal",
		},
	)
	return err
}
