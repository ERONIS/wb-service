package workflow

import (
	"context"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type racingInitializationRepository struct {
	Repository
	phase              transfer_model.Phase
	failureCalls       int
	activateDuringMark bool
}

type racingInitializationBatchReader struct {
	BatchReader
	beforeError func()
}

func (repository *racingInitializationRepository) FindByID(
	_ context.Context,
	_ transfer_model.TransferID,
) (transfer_model.Transfer, error) {
	return transfer_model.Transfer{
		ID: 7, BatchID: 9, Phase: repository.phase,
		ItemsCount: 1, GroupsCount: 1,
	}, nil
}

func (repository *racingInitializationRepository) MarkInitializationFailed(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ transfer_model.TransferID,
	_ string,
) error {
	repository.failureCalls++
	if repository.activateDuringMark {
		repository.phase = transfer_model.PhaseActive
		return ErrInitializationConflict
	}
	repository.phase = transfer_model.PhaseFinished
	return nil
}

func (reader *racingInitializationBatchReader) GetReadyBatch(
	_ context.Context,
	_ cardpipeline.BatchID,
) (cardpipeline.ReadyBatch, error) {
	if reader.beforeError != nil {
		reader.beforeError()
	}
	return cardpipeline.ReadyBatch{}, core_errors.ErrNotFound
}

func TestInitializeDoesNotFailTransferAfterConcurrentHandoff(t *testing.T) {
	for _, test := range []struct {
		name                string
		activateBeforeCheck bool
		activateDuringMark  bool
		wantFailureCalls    int
	}{
		{name: "активация при чтении пачки", activateBeforeCheck: true},
		{name: "активация при записи ошибки", activateDuringMark: true, wantFailureCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &racingInitializationRepository{
				phase:              transfer_model.PhaseInitializing,
				activateDuringMark: test.activateDuringMark,
			}
			reader := &racingInitializationBatchReader{}
			if test.activateBeforeCheck {
				reader.beforeError = func() {
					repository.phase = transfer_model.PhaseActive
				}
			}
			service := &Service{
				repository:  repository,
				batchReader: reader,
				uow:         pollingUOW{},
			}

			progress, err := service.Initialize(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if progress {
				t.Fatal("повторный обработчик не должен сообщать о новой инициализации")
			}
			if repository.phase != transfer_model.PhaseActive {
				t.Fatalf("ожидалась фаза active, получена %s", repository.phase)
			}
			if repository.failureCalls != test.wantFailureCalls {
				t.Fatalf("ожидалось %d попыток записать ошибку, получено %d", test.wantFailureCalls, repository.failureCalls)
			}
		})
	}
}
