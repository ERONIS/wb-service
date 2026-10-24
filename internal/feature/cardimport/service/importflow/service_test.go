package cardimport_flow

import (
	"context"
	"errors"
	"fmt"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_batch "github.com/ERONIS/wb-service/internal/feature/cardimport/service/batch"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
)

type memoryTestParser struct{}

type memoryTestRepository struct {
	finalized []cardimport_contract.FinalizedSession
	batches   map[cardpipeline.BatchID]cardimport_batch.BatchHeader
}

func (*memoryTestRepository) HasAuthor(context.Context, int64) (bool, error) {
	return true, nil
}

type memoryTestUOW struct {
	failNext bool
}

func (memoryTestParser) Parse(cardimport_intake.FileID, io.Reader) (cardimport_intake.ParsedFile, error) {
	return cardimport_intake.ParsedFile{}, errors.New("parser is not used by this fixture")
}

func (repository *memoryTestRepository) PersistFinalized(
	_ context.Context, _ core_postgres_transaction.DBTX, finalized cardimport_contract.FinalizedSession,
) (cardimport_batch.BatchHeader, error) {
	repository.finalized = append(repository.finalized, finalized)
	header := cardimport_batch.BatchHeader{
		ID: cardpipeline.BatchID(len(repository.finalized)), SourceSessionID: finalized.Session.ID,
		ItemsCount:       len(finalized.Items),
		GroupsCount:      finalized.GroupsCount,
		AuthorTelegramID: finalized.User.TelegramID,
		FinalizedAt:      time.Now().UTC(),
	}
	if err := header.Validate(); err != nil {
		return cardimport_batch.BatchHeader{}, err
	}
	repository.batches[header.ID] = header
	return header, nil
}

func (repository *memoryTestRepository) GetBatch(_ context.Context, id cardpipeline.BatchID) (cardimport_batch.BatchHeader, error) {
	return repository.batches[id], nil
}

func (repository *memoryTestRepository) ListFinalizedBatches(context.Context, *cardpipeline.BatchCursor, int) ([]cardimport_batch.BatchHeader, error) {
	return nil, nil
}

func (repository *memoryTestRepository) ListBatchItems(_ context.Context, batchID cardpipeline.BatchID, afterPosition int, limit int) ([]cardimport_batch.BatchItem, error) {
	items := repository.finalized[int(batchID)-1].Items
	page := make([]cardimport_batch.BatchItem, 0, limit)
	for index, item := range items {
		if item.Position <= afterPosition {
			continue
		}
		item.ID = cardpipeline.BatchItemID(index + 1)
		item.BatchID = batchID
		page = append(page, item)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}

func (uow *memoryTestUOW) WithinTransaction(
	ctx context.Context, fn func(context.Context, core_postgres_transaction.DBTX) error,
) error {
	if uow.failNext {
		uow.failNext = false
		return errors.New("transaction failed")
	}
	return fn(ctx, nil)
}

func TestImportStaysInMemoryUntilFinalize(t *testing.T) {
	ctx := context.Background()
	repository := &memoryTestRepository{batches: make(map[cardpipeline.BatchID]cardimport_batch.BatchHeader)}
	uow := &memoryTestUOW{failNext: true}
	service := New(repository, memoryTestParser{}, uow)
	user := domain.CreateUser(12345, "Тест Пользователь")
	session, err := service.Begin(ctx, cardimport_contract.BeginCommand{AuthorTelegramID: user.TelegramID})
	if err != nil {
		t.Fatal(err)
	}
	addMemoryTestFile(t, service, session, 1, memoryTestCard("sku-1", "barcode-1", 2))
	if len(repository.finalized) != 0 {
		t.Fatal("сессия записана до финализации")
	}
	view, err := service.GetView(ctx, cardimport_contract.SessionCommand{AuthorTelegramID: user.TelegramID, SessionID: session.ID})
	if err != nil || !view.Ready || view.CardsCount != 1 {
		t.Fatalf("сессия не готова: view=%+v err=%v", view, err)
	}
	command := cardimport_contract.FinalizeCommand{SessionID: session.ID, ExpectedRevision: view.Revision, IdempotencyKey: "test-finalize"}
	if _, err := service.Finalize(ctx, user, command); err == nil {
		t.Fatal("ожидалась ошибка транзакции")
	}
	view, err = service.GetView(ctx, cardimport_contract.SessionCommand{AuthorTelegramID: user.TelegramID, SessionID: session.ID})
	if err != nil || !view.Ready || len(repository.finalized) != 0 {
		t.Fatalf("ошибка транзакции изменила сессию: view=%+v err=%v", view, err)
	}
	header, err := service.Finalize(ctx, user, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.finalized) != 1 || repository.finalized[0].Items[0].VendorCode != "sku-1" {
		t.Fatalf("неверный пакет после финализации: %+v", repository.finalized)
	}
	previous, err := service.Finalize(ctx, user, command)
	if err != nil || previous.ID != header.ID || len(repository.finalized) != 1 {
		t.Fatalf("повторная финализация не идемпотентна: header=%+v err=%v", previous, err)
	}
	restarted := New(repository, memoryTestParser{}, &memoryTestUOW{})
	if _, err := restarted.GetActiveView(ctx, user.TelegramID); !errors.Is(err, core_errors.ErrNotFound) {
		t.Fatalf("сессия пережила перезапуск: %v", err)
	}
	stored, err := restarted.GetBatch(ctx, header.ID)
	if err != nil || stored.ID != header.ID {
		t.Fatalf("готовый пакет недоступен после перезапуска: %+v %v", stored, err)
	}
	ready, err := restarted.GetReadyBatch(ctx, header.ID)
	if err != nil || ready.Header.AuthorTelegramID != user.TelegramID ||
		len(ready.Items) != 1 || ready.Items[0].Payload.Variant.VendorCode != "sku-1" {
		t.Fatalf("готовые карточки недоступны transfer после перезапуска: %+v %v", ready, err)
	}
	repository.finalized[0].Items[0].Payload.Variant.VendorCode = "changed"
	if _, err := restarted.GetReadyBatch(ctx, header.ID); !errors.Is(err, core_errors.ErrConflict) {
		t.Fatalf("изменённый после финализации пакет принят: %v", err)
	}
}

func TestContinueKeepsOneCardWhenFilesShareBarcode(t *testing.T) {
	ctx := context.Background()
	service := New(&memoryTestRepository{batches: make(map[cardpipeline.BatchID]cardimport_batch.BatchHeader)}, memoryTestParser{}, &memoryTestUOW{})
	session, err := service.Begin(ctx, cardimport_contract.BeginCommand{AuthorTelegramID: 12345})
	if err != nil {
		t.Fatal(err)
	}
	addMemoryTestFile(t, service, session, 1, memoryTestCard("sku-1", "shared", 2))
	addMemoryTestFile(t, service, session, 2, memoryTestCard("sku-2", "shared", 3))
	view, err := service.GetView(ctx, cardimport_contract.SessionCommand{AuthorTelegramID: 12345, SessionID: session.ID})
	if err != nil || view.ErrorsCount != 1 || view.Ready {
		t.Fatalf("межфайловый дубль не найден: view=%+v err=%v", view, err)
	}
	continued, err := service.Continue(ctx, cardimport_contract.ContinueCommand{
		AuthorTelegramID: 12345, SessionID: session.ID, ExpectedRevision: view.Revision,
	})
	if err != nil || !continued.Ready || continued.CardsCount != 1 || continued.ErrorsCount != 0 {
		t.Fatalf("пропуск дубля неверен: view=%+v err=%v", continued, err)
	}
}

func TestConcurrentFilesKeepSessionRevision(t *testing.T) {
	ctx := context.Background()
	service := New(&memoryTestRepository{batches: make(map[cardpipeline.BatchID]cardimport_batch.BatchHeader)}, memoryTestParser{}, &memoryTestUOW{})
	session, err := service.Begin(ctx, cardimport_contract.BeginCommand{AuthorTelegramID: 12345})
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]cardimport_contract.ParseFileCommand, 2)
	for index := range commands {
		file, err := service.ReserveFile(ctx, cardimport_contract.ReserveFileCommand{
			AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID,
			TelegramFileID:       fmt.Sprintf("telegram-%d", index),
			TelegramFileUniqueID: fmt.Sprintf("unique-%d", index),
			TelegramMessageID:    int64(index + 1), OriginalFilename: "test.xlsx", DeclaredSize: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.StoreFile(ctx, cardimport_contract.StoreFileCommand{
			AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID, FileID: file.ID,
		}, strings.NewReader("xlsx")); err != nil {
			t.Fatal(err)
		}
		commands[index] = cardimport_contract.ParseFileCommand{AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID, FileID: file.ID}
		if _, err := service.sessions.Claim(ctx, commands[index]); err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	failures := make([]error, len(commands))
	for index, command := range commands {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, failures[index] = service.sessions.SaveParsed(ctx, command, cardimport_intake.AggregatedFile{
				FileID: command.FileID,
				Cards:  []cardpipeline.Card{memoryTestCard(fmt.Sprintf("sku-%d", index), fmt.Sprintf("barcode-%d", index), index+2)},
			})
		}()
	}
	workers.Wait()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	view, err := service.GetView(ctx, cardimport_contract.SessionCommand{AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID})
	if err != nil || view.Revision != 2 || view.CardsCount != 2 || !view.Ready {
		t.Fatalf("параллельная обработка потеряла изменение: view=%+v err=%v", view, err)
	}
}

func addMemoryTestFile(t *testing.T, service *Service, session cardimport_intake.Session, position int, card cardpipeline.Card) {
	t.Helper()
	ctx := context.Background()
	file, err := service.ReserveFile(ctx, cardimport_contract.ReserveFileCommand{
		AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID,
		TelegramFileID:       fmt.Sprintf("telegram-%d", position),
		TelegramFileUniqueID: fmt.Sprintf("unique-%d", position),
		TelegramMessageID:    int64(position), OriginalFilename: fmt.Sprintf("%d.xlsx", position),
		DeclaredSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StoreFile(ctx, cardimport_contract.StoreFileCommand{
		AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID, FileID: file.ID,
	}, strings.NewReader("xlsx")); err != nil {
		t.Fatal(err)
	}
	command := cardimport_contract.ParseFileCommand{AuthorTelegramID: session.AuthorTelegramID, SessionID: session.ID, FileID: file.ID}
	if _, err := service.sessions.Claim(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.sessions.SaveParsed(ctx, command, cardimport_intake.AggregatedFile{FileID: file.ID, Cards: []cardpipeline.Card{card}}); err != nil {
		t.Fatal(err)
	}
}

func memoryTestCard(vendor, barcode string, row int) cardpipeline.Card {
	return cardpipeline.Card{
		SourceRows: []int{row}, Group: "1", Category: "Одежда",
		Variant: cardpipeline.Variant{
			VendorCode: vendor, Sizes: []cardpipeline.Size{{TechSize: "M", SKUs: []string{barcode}}},
		},
	}
}
