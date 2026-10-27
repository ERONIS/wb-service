package workflow

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

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

type resumingRepository struct {
	Repository
	transfer     transfer_model.Transfer
	initialized  int
	created      int
	foundByBatch int
}

type resumingBatchReader struct {
	BatchReader
	batch cardpipeline.ReadyBatch
}

type discoveringRepository struct {
	Repository
	attempts map[cardpipeline.BatchID]int
	created  []cardpipeline.BatchID
}

type discoveringBatchReader struct {
	BatchReader
	headers   []cardpipeline.ReadyBatchHeader
	pageCalls int
}

type flakyTargetRegistry struct {
	calls int
}

type interruptedDiscoveryRepository struct {
	*discoveringRepository
}

type interruptedBatchReader struct {
	*discoveringBatchReader
	read      []cardpipeline.BatchID
	readError error
}

func (repository *pollingRepository) ListInitializing(
	_ context.Context,
	afterID transfer_model.TransferID,
	_ int,
) ([]transfer_model.Transfer, error) {
	if afterID != 0 {
		return nil, nil
	}
	return []transfer_model.Transfer{{ID: 1, Phase: transfer_model.PhaseInitializing}}, nil
}

func (*pollingRepository) FindByID(context.Context, transfer_model.TransferID) (transfer_model.Transfer, error) {
	return transfer_model.Transfer{ID: 1, Phase: transfer_model.PhaseInitializing}, nil
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

func (repository *resumingRepository) ListInitializing(
	_ context.Context,
	afterID transfer_model.TransferID,
	_ int,
) ([]transfer_model.Transfer, error) {
	if repository.transfer.Phase != transfer_model.PhaseInitializing || afterID >= repository.transfer.ID {
		return nil, nil
	}
	return []transfer_model.Transfer{repository.transfer}, nil
}

func (repository *resumingRepository) FindByID(
	_ context.Context,
	_ transfer_model.TransferID,
) (transfer_model.Transfer, error) {
	return repository.transfer, nil
}

func (repository *resumingRepository) FindByBatch(
	_ context.Context,
	_ cardpipeline.BatchID,
) (transfer_model.Transfer, error) {
	repository.foundByBatch++
	return repository.transfer, nil
}

func (repository *resumingRepository) Create(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	_ CreateTransfer,
) (transfer_model.Transfer, error) {
	repository.created++
	return repository.transfer, nil
}

func (repository *resumingRepository) Initialize(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	command InitializeTransferCommand,
) (bool, error) {
	if err := command.Validate(); err != nil {
		return false, err
	}
	repository.initialized++
	repository.transfer.Phase = transfer_model.PhaseActive
	return true, nil
}

func (reader *resumingBatchReader) GetReadyBatch(
	_ context.Context,
	_ cardpipeline.BatchID,
) (cardpipeline.ReadyBatch, error) {
	return reader.batch, nil
}

func (reader *resumingBatchReader) ListReadyBatches(
	_ context.Context,
	after *cardpipeline.BatchCursor,
	_ int,
) ([]cardpipeline.ReadyBatchHeader, error) {
	if after != nil {
		return nil, nil
	}
	return []cardpipeline.ReadyBatchHeader{reader.batch.Header}, nil
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

func TestProcessPendingResumesInitializationOnceWhenBatchIsDiscoveredAgain(t *testing.T) {
	finalizedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	batch := cardpipeline.ReadyBatch{
		Header: cardpipeline.ReadyBatchHeader{
			ID: 9, ItemsCount: 1, GroupsCount: 1,
			AuthorTelegramID: 10, FinalizedAt: finalizedAt,
		},
		Items: []cardpipeline.ReadyBatchItem{{
			ID: 11, BatchID: 9, Position: 1,
			SourceGroupNumber: 1,
			Payload: cardpipeline.Card{
				SourceRows: []int{1},
				Group:      "group-1",
				Variant:    cardpipeline.Variant{VendorCode: "article-1"},
			},
		}},
	}
	repository := &resumingRepository{transfer: transfer_model.Transfer{
		ID: 7, BatchID: batch.Header.ID, Phase: transfer_model.PhaseInitializing,
		ItemsCount: 1, GroupsCount: 1,
	}}
	service := &Service{
		repository: repository, batchReader: &resumingBatchReader{batch: batch}, uow: pollingUOW{},
	}
	notifications := 0
	for range 2 {
		if err := service.ProcessPendingWithNotify(context.Background(), func() {
			notifications++
		}); err != nil {
			t.Fatal(err)
		}
	}
	if repository.transfer.Phase != transfer_model.PhaseActive {
		t.Fatalf("после восстановления ожидалась фаза active, получена %s", repository.transfer.Phase)
	}
	if repository.initialized != 1 || notifications != 1 {
		t.Fatalf("ожидались одна инициализация и одно уведомление, получены %d и %d", repository.initialized, notifications)
	}
	if repository.foundByBatch != 2 || repository.created != 0 {
		t.Fatalf("повторное обнаружение пачки не должно создавать перенос: найдено %d раз, создано %d", repository.foundByBatch, repository.created)
	}
}

func (repository *discoveringRepository) FindByBatch(
	_ context.Context,
	batchID cardpipeline.BatchID,
) (transfer_model.Transfer, error) {
	repository.attempts[batchID]++
	if batchID == 1 && len(repository.created) == 0 {
		return transfer_model.Transfer{}, core_errors.ErrNotFound
	}
	return transfer_model.Transfer{ID: transfer_model.TransferID(batchID), Phase: transfer_model.PhaseActive}, nil
}

func (*discoveringRepository) FindByID(
	_ context.Context,
	transferID transfer_model.TransferID,
) (transfer_model.Transfer, error) {
	return transfer_model.Transfer{ID: transferID, Phase: transfer_model.PhaseActive}, nil
}

func (repository *discoveringRepository) Create(
	_ context.Context,
	_ core_postgres_transaction.DBTX,
	command CreateTransfer,
) (transfer_model.Transfer, error) {
	repository.created = append(repository.created, command.Batch.ID)
	return transfer_model.Transfer{ID: transfer_model.TransferID(command.Batch.ID), Phase: transfer_model.PhaseActive}, nil
}

func (reader *discoveringBatchReader) GetReadyBatchHeader(
	_ context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatchHeader, error) {
	for _, header := range reader.headers {
		if header.ID == batchID {
			return header, nil
		}
	}
	return cardpipeline.ReadyBatchHeader{}, core_errors.ErrNotFound
}

func (reader *discoveringBatchReader) ListReadyBatches(
	_ context.Context,
	after *cardpipeline.BatchCursor,
	limit int,
) ([]cardpipeline.ReadyBatchHeader, error) {
	reader.pageCalls++
	headers := append([]cardpipeline.ReadyBatchHeader(nil), reader.headers...)
	sort.Slice(headers, func(i, j int) bool {
		if headers[i].FinalizedAt.Equal(headers[j].FinalizedAt) {
			return headers[i].ID < headers[j].ID
		}
		return headers[i].FinalizedAt.Before(headers[j].FinalizedAt)
	})
	page := make([]cardpipeline.ReadyBatchHeader, 0, limit)
	for _, header := range headers {
		if after != nil && (header.FinalizedAt.Before(after.FinalizedAt) ||
			(header.FinalizedAt.Equal(after.FinalizedAt) && header.ID <= after.BatchID)) {
			continue
		}
		page = append(page, header)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}

func (registry *flakyTargetRegistry) FanoutTargets(
	context.Context,
) ([]transfer_model.MutationTarget, error) {
	registry.calls++
	if registry.calls == 1 {
		return nil, core_errors.InvalidArgument("временная ошибка доступа к кабинету")
	}
	return []transfer_model.MutationTarget{{}}, nil
}

func (*interruptedDiscoveryRepository) ListInitializing(
	_ context.Context,
	afterID transfer_model.TransferID,
	_ int,
) ([]transfer_model.Transfer, error) {
	transfers := []transfer_model.Transfer{
		{ID: 100, BatchID: 100, Phase: transfer_model.PhaseInitializing},
		{ID: 200, BatchID: 200, Phase: transfer_model.PhaseInitializing},
	}
	page := make([]transfer_model.Transfer, 0, len(transfers))
	for _, transfer := range transfers {
		if transfer.ID > afterID {
			page = append(page, transfer)
		}
	}
	return page, nil
}

func (*interruptedDiscoveryRepository) FindByID(
	_ context.Context,
	transferID transfer_model.TransferID,
) (transfer_model.Transfer, error) {
	if transferID == 100 || transferID == 200 {
		return transfer_model.Transfer{
			ID: transferID, BatchID: cardpipeline.BatchID(transferID),
			Phase: transfer_model.PhaseInitializing,
		}, nil
	}
	return transfer_model.Transfer{ID: transferID, Phase: transfer_model.PhaseActive}, nil
}

func (reader *interruptedBatchReader) GetReadyBatch(
	_ context.Context,
	batchID cardpipeline.BatchID,
) (cardpipeline.ReadyBatch, error) {
	reader.read = append(reader.read, batchID)
	return cardpipeline.ReadyBatch{}, reader.readError
}

func TestDiscoveryFindsLateBatchBeforePreviousCursor(t *testing.T) {
	finalizedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	repository := &discoveringRepository{attempts: make(map[cardpipeline.BatchID]int)}
	reader := &discoveringBatchReader{headers: []cardpipeline.ReadyBatchHeader{
		{ID: 2, ItemsCount: 1, GroupsCount: 1, AuthorTelegramID: 10, FinalizedAt: finalizedAt.Add(time.Hour)},
	}}
	service := &Service{repository: repository, batchReader: reader}
	if err := service.discoverFinalizedBatches(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	reader.headers = append(reader.headers, cardpipeline.ReadyBatchHeader{
		ID: 3, ItemsCount: 1, GroupsCount: 1, AuthorTelegramID: 10, FinalizedAt: finalizedAt,
	})
	if err := service.discoverFinalizedBatches(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if repository.attempts[3] != 1 {
		t.Fatalf("поздно записанная пачка с более ранним finalized_at не найдена: попыток %d", repository.attempts[3])
	}
}

func TestDiscoveryRetriesRejectedBatchAndContinuesNextPage(t *testing.T) {
	finalizedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	headers := make([]cardpipeline.ReadyBatchHeader, 0, pollingPageSize+1)
	for id := 1; id <= pollingPageSize+1; id++ {
		headers = append(headers, cardpipeline.ReadyBatchHeader{
			ID: cardpipeline.BatchID(id), ItemsCount: 1, GroupsCount: 1,
			AuthorTelegramID: 10, FinalizedAt: finalizedAt.Add(time.Duration(id) * time.Second),
		})
	}
	repository := &discoveringRepository{attempts: make(map[cardpipeline.BatchID]int)}
	reader := &discoveringBatchReader{headers: headers}
	registry := &flakyTargetRegistry{}
	service := &Service{
		repository: repository, batchReader: reader, targetRegistry: registry,
		uow: pollingUOW{}, capacity: transfer_model.CapacityPolicy{
			MaxItems: 10, MaxGroups: 10, MaxTargets: 10, MaxItemTargets: 10, MaxGroupTargets: 10,
		},
	}
	err := service.discoverFinalizedBatches(context.Background(), nil)
	if !errors.Is(err, core_errors.ErrInvalidArgument) {
		t.Fatalf("ошибка первой пачки потеряна: %v", err)
	}
	if repository.attempts[cardpipeline.BatchID(pollingPageSize+1)] != 1 || reader.pageCalls != 2 {
		t.Fatalf("последняя пачка не обработана после ошибки первой: попыток %d, страниц %d",
			repository.attempts[cardpipeline.BatchID(pollingPageSize+1)], reader.pageCalls)
	}
	if err := service.discoverFinalizedBatches(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if repository.attempts[1] != 2 || len(repository.created) != 1 || repository.created[0] != 1 {
		t.Fatalf("первая пачка не повторена после восстановления кабинета: попыток %d, создано %v",
			repository.attempts[1], repository.created)
	}
}

func TestProcessPendingContinuesDiscoveryAfterResumeErrors(t *testing.T) {
	temporaryError := errors.New("temporary read failure")
	repository := &interruptedDiscoveryRepository{
		discoveringRepository: &discoveringRepository{attempts: make(map[cardpipeline.BatchID]int)},
	}
	reader := &interruptedBatchReader{
		discoveringBatchReader: &discoveringBatchReader{
			headers: []cardpipeline.ReadyBatchHeader{{
				ID: 300, ItemsCount: 1, GroupsCount: 1, AuthorTelegramID: 10,
				FinalizedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			}},
		},
		readError: temporaryError,
	}
	service := &Service{repository: repository, batchReader: reader}
	err := service.ProcessPendingWithNotify(context.Background(), nil)
	if !errors.Is(err, temporaryError) {
		t.Fatalf("ошибка восстановления не возвращена: %v", err)
	}
	if len(reader.read) != 2 || reader.read[0] != 100 || reader.read[1] != 200 {
		t.Fatalf("не все ожидающие инициализации проверены: %v", reader.read)
	}
	if repository.attempts[300] != 1 {
		t.Fatalf("ошибка восстановления остановила поиск новых пачек: попыток %d", repository.attempts[300])
	}
}
