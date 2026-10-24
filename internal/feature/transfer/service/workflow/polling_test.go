package workflow

import (
	"context"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type pollingRepository struct {
	Repository
	failed bool
}

type pollingBatchReader struct{ BatchReader }

type pollingUOW struct{}

func (repository *pollingRepository) ListInitializing(
	_ context.Context,
	afterID transfer_model.TransferID,
	_ int,
) ([]transfer_model.Transfer, error) {
	if afterID != 0 {
		return nil, nil
	}
	return []transfer_model.Transfer{{ID: 1, Phase: PhaseInitializing}}, nil
}

func (*pollingRepository) FindByID(context.Context, transfer_model.TransferID) (transfer_model.Transfer, error) {
	return transfer_model.Transfer{ID: 1, Phase: PhaseInitializing}, nil
}

func (repository *pollingRepository) MarkInitializationFailed(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ transfer_model.TransferID,
	_ string,
) error {
	repository.failed = true
	return nil
}

func (*pollingBatchReader) GetReadyBatchHeader(context.Context, cardpipeline.BatchID) (cardpipeline.ReadyBatchHeader, error) {
	return cardpipeline.ReadyBatchHeader{}, core_errors.ErrNotFound
}

func (*pollingBatchReader) GetReadyBatch(context.Context, cardpipeline.BatchID) (cardpipeline.ReadyBatch, error) {
	return cardpipeline.ReadyBatch{}, core_errors.ErrNotFound
}

func (*pollingBatchReader) ListReadyBatches(
	context.Context,
	*cardpipeline.BatchCursor,
	int,
) ([]cardpipeline.ReadyBatchHeader, error) {
	return nil, nil
}

func (pollingUOW) WithinTransaction(
	ctx context.Context,
	fn func(context.Context, core_postgres_transaction.DBTX) error,
) error {
	return fn(ctx, nil)
}

func TestProcessPendingDoesNotWakePreparationAfterInitializationFailure(t *testing.T) {
	repository := &pollingRepository{}
	service := &Service{
		repository:  repository,
		batchReader: &pollingBatchReader{},
		uow:         pollingUOW{},
	}
	notifications := 0
	err := service.ProcessPendingWithNotify(context.Background(), func() {
		notifications++
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.failed {
		t.Fatal("терминальная ошибка инициализации не сохранена")
	}
	if notifications != 0 {
		t.Fatalf("завершённая с ошибкой инициализация не должна будить подготовку, получено %d уведомлений", notifications)
	}
}
