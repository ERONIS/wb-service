package execution

import (
	"context"
	"errors"
	"testing"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_authorization "github.com/ERONIS/wb-service/internal/feature/transfer/service/authorization"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type retryTestTransaction struct {
	core_postgres_transaction.DBTX
}

type retryTestUnitOfWork struct {
	commits int
}

type retryTestRepository struct {
	ProductJournalRepository
	action              ProductAction
	observations        int
	reconciliationPolls int
	retries             int
}

type retryTestAuthorization struct {
	LiveAuthorizationVerifier
	err    error
	checks []transfer_authorization.LiveAuthorizationCheck
}

func (unit *retryTestUnitOfWork) WithinTransaction(
	ctx context.Context,
	fn func(context.Context, core_postgres_transaction.DBTX) error,
) error {
	if err := fn(ctx, retryTestTransaction{}); err != nil {
		return err
	}
	unit.commits++
	return nil
}

func (repository *retryTestRepository) LockProductAction(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ ProductActionCandidate,
) (ProductAction, error) {
	return repository.action, nil
}

func (repository *retryTestRepository) InsertActionObservation(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ transfer_model.TransferID,
	_ string,
	_ cardpublication_model.ObservationDraft,
) (int64, error) {
	repository.observations++
	return 1, nil
}

func (repository *retryTestRepository) RecordProductReconciliationPoll(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ ProductAction,
	_ int64,
	_ []MemberReconciliationResult,
) error {
	repository.reconciliationPolls++
	return nil
}

func (repository *retryTestRepository) BeginProductRetry(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ BeginProductRetryCommand,
) (ProductRetry, error) {
	repository.retries++
	return ProductRetry{}, nil
}

func (authorization *retryTestAuthorization) LockValidProductRetry(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	check transfer_authorization.LiveAuthorizationCheck,
) (transfer_authorization.LiveAuthorizationEvidence, error) {
	authorization.checks = append(authorization.checks, check)
	return transfer_authorization.LiveAuthorizationEvidence{}, authorization.err
}

func TestProductRetryRequiresCurrentAuthorization(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "истёк срок", err: transfer_authorization.ErrLiveExpired},
		{name: "авторизация отозвана", err: transfer_authorization.ErrLiveAuthorization},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := ProductAction{
				ProductActionCandidate: ProductActionCandidate{
					TransferID:            10,
					PlanID:                20,
					ActionID:              30,
					TargetID:              40,
					AuthorizationID:       50,
					AuthorizationRevision: 2,
				},
				State: "reconciling",
			}
			repository := &retryTestRepository{action: action}
			authorization := &retryTestAuthorization{err: test.err}
			unit := &retryTestUnitOfWork{}
			dispatcher := &ProductDispatcher{
				repository:    repository,
				authorization: authorization,
				uow:           unit,
			}
			retried, err := dispatcher.retryAfterEmptyResponse(
				context.Background(), action.ProductActionCandidate, action, TargetedRecheck{},
			)
			if err != nil || retried {
				t.Fatalf("повтор не должен начаться: retried=%t err=%v", retried, err)
			}
			if unit.commits != 1 {
				t.Fatalf("изменение срока авторизации должно сохраняться: commits=%d", unit.commits)
			}
			if len(authorization.checks) != 1 ||
				authorization.checks[0].AuthorizationID != action.AuthorizationID ||
				authorization.checks[0].ActionID != action.ActionID ||
				authorization.checks[0].ExpectedAuthorizationRevision != action.AuthorizationRevision {
				t.Fatalf("проверена неверная авторизация: %+v", authorization.checks)
			}
			if repository.observations != 0 || repository.reconciliationPolls != 0 || repository.retries != 0 {
				t.Fatalf("журнал повторной отправки изменился без допуска: %+v", repository)
			}
		})
	}
}

func TestProductRetryPropagatesVerificationFailure(t *testing.T) {
	action := ProductAction{ProductActionCandidate: ProductActionCandidate{
		TransferID: 10, PlanID: 20, ActionID: 30, TargetID: 40, AuthorizationID: 50,
	}}
	repository := &retryTestRepository{action: action}
	verificationErr := errors.New("временная ошибка базы")
	dispatcher := &ProductDispatcher{
		repository:    repository,
		authorization: &retryTestAuthorization{err: verificationErr},
		uow:           &retryTestUnitOfWork{},
	}
	retried, err := dispatcher.retryAfterEmptyResponse(
		context.Background(), action.ProductActionCandidate, action, TargetedRecheck{},
	)
	if retried || !errors.Is(err, verificationErr) || repository.retries != 0 {
		t.Fatalf("ошибка проверки должна остановить повтор: retried=%t err=%v", retried, err)
	}
}
