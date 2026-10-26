package cardimport_postgres_repository

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

type finalizationTestPool struct {
	*pgxpool.Pool
}

func (pool *finalizationTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

func TestPostgresPersistFinalizedIsAtomic(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	pool := &finalizationTestPool{Pool: connection}
	for _, name := range []string{"wb.card_import_sessions", "wb.card_import_files", "wb.card_import_issues"} {
		var table *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", name).Scan(&table); err != nil {
			t.Fatal(err)
		}
		if table != nil {
			t.Fatalf("в схеме осталась таблица %s", name)
		}
	}
	now := time.Now().UTC()
	sessionID := cardimport_model.SessionID(now.UnixNano())
	fileID := cardimport_model.FileID(now.UnixNano() + 1)
	user := domain.CreateUser(now.UnixNano()+2, "Тест Пользователь")
	card := cardpipeline.Card{
		SourceRows: []int{2}, Group: "1", Category: "Одежда",
		Variant: cardpipeline.Variant{
			VendorCode: "test-sku",
			Sizes:      []cardpipeline.Size{{TechSize: "M", SKUs: []string{"test-barcode"}}},
		},
	}
	item := cardimport_model.BatchItem{
		Position: 1, SourceFileID: fileID,
		SourceGroupNumber: 1,
		Payload:           card,
	}
	finalized := cardimport_contract.FinalizedSession{
		Session: cardimport_model.Session{
			ID: sessionID, AuthorTelegramID: user.TelegramID,
			Status:   cardimport_model.SessionStatusCollecting,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		SourceFiles: []cardimport_contract.FinalizedSourceFile{{ID: fileID, SessionID: sessionID}},
		Items:       []cardimport_model.BatchItem{item}, GroupsCount: 1, User: user,
		Command: cardimport_contract.FinalizeCommand{
			SessionID: sessionID, ExpectedRevision: 1, IdempotencyKey: "test",
		},
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	repository := New(pool)
	header, err := repository.PersistFinalized(ctx, tx, finalized)
	if err != nil {
		t.Fatal(err)
	}
	if header.ItemsCount != 1 || header.GroupsCount != 1 {
		t.Fatal("неверные размеры сохранённой пачки")
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM wb.card_batch_items WHERE batch_id = $1", header.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("записано %d элементов вместо одного", count)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM wb.card_batches WHERE source_session_id = $1", sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("отмена транзакции оставила пачку в БД")
	}

	committedTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer committedTx.Rollback(context.Background())
	header, err = repository.PersistFinalized(ctx, committedTx, finalized)
	if err != nil {
		t.Fatal(err)
	}
	if err := committedTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetBatch(ctx, header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != header.ID || stored.SourceSessionID != sessionID {
		t.Fatal("сохранённая пачка не совпадает с исходной сессией")
	}
	expectedPayload, err := json.Marshal(item.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var storedPayloadMatches bool
	var storedGroupNumber int
	if err := pool.QueryRow(ctx, `
		SELECT payload = $2::jsonb, source_group_number
		FROM wb.card_batch_items
		WHERE batch_id = $1 AND position = 1;
	`, header.ID, string(expectedPayload)).Scan(&storedPayloadMatches, &storedGroupNumber); err != nil {
		t.Fatal(err)
	}
	if !storedPayloadMatches || storedGroupNumber != item.SourceGroupNumber {
		t.Fatal("сохранённая карточка не совпадает с исходными данными")
	}

	replayTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replayTx.Rollback(context.Background())
	replayed, err := repository.PersistFinalized(ctx, replayTx, finalized)
	if err != nil || replayed.ID != header.ID {
		t.Fatalf("повторная финализация не вернула исходную пачку: %v", err)
	}
}
