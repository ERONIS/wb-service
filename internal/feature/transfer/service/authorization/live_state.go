package authorization

import (
	"context"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func (service *LiveAuthorizationService) LockValid(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	check LiveAuthorizationCheck,
) (LiveAuthorizationEvidence, error) {
	if !service.live {
		return LiveAuthorizationEvidence{}, ErrLiveModeDisabled
	}
	if ctx == nil || tx == nil {
		return LiveAuthorizationEvidence{}, ErrLiveAuthorization
	}
	if err := check.validate(); err != nil {
		return LiveAuthorizationEvidence{}, err
	}
	return service.repository.LockValidLive(ctx, tx, service.now().UTC(), check)
}

func (service *LiveAuthorizationService) LockValidProductRetry(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	check LiveAuthorizationCheck,
) (LiveAuthorizationEvidence, error) {
	if !service.live {
		return LiveAuthorizationEvidence{}, ErrLiveModeDisabled
	}
	if ctx == nil || tx == nil {
		return LiveAuthorizationEvidence{}, ErrLiveAuthorization
	}
	if err := check.validate(); err != nil {
		return LiveAuthorizationEvidence{}, err
	}
	return service.repository.LockValidProductRetry(ctx, tx, service.now().UTC(), check)
}

func (service *LiveAuthorizationService) SupersedeWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	return service.changeStateWithin(ctx, tx, LiveAuthorizationSuperseded, command)
}

func (service *LiveAuthorizationService) CloseWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	return service.changeStateWithin(ctx, tx, LiveAuthorizationClosed, command)
}

func (service *LiveAuthorizationService) changeStateWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	state LiveAuthorizationState,
	command ChangeLiveAuthorizationStateCommand,
) (LiveAuthorization, error) {
	command = command.normalized()
	if ctx == nil || tx == nil {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	if err := command.validate(); err != nil {
		return LiveAuthorization{}, err
	}
	if state != LiveAuthorizationSuperseded && state != LiveAuthorizationClosed {
		return LiveAuthorization{}, ErrLiveAuthorization
	}
	authorization, err := service.repository.ChangeLiveState(
		ctx,
		tx,
		service.now().UTC(),
		state,
		command,
		fmt.Sprintf(
			"system:%s-live:%d:%d",
			state,
			command.AuthorizationID,
			command.ExpectedRevision,
		),
	)
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("change live authorization state: %w", err)
	}
	return authorization, nil
}
