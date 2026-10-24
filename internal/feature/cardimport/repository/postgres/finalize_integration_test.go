package cardimport_postgres_repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
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
	var issuesTable *string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('wb.card_import_issues')::text").Scan(&issuesTable); err != nil {
		t.Fatal(err)
	}
	if issuesTable != nil {
			t.Fatal("в исходной схеме осталась таблица card_import_issues")
	}
	now := time.Now().UTC()
	sessionID := cardimport_service.SessionID(now.UnixNano())
	fileID := cardimport_service.FileID(now.UnixNano() + 1)
	user := domain.CreateUser(now.UnixNano()+2, "Тест Пользователь")
	card := cardpipeline.Card{
		SourceRows: []int{2}, Category: "Одежда",
		Variant: cardpipeline.Variant{
			VendorCode: "test-sku",
			Sizes:      []cardpipeline.Size{{TechSize: "M", SKUs: []string{"test-barcode"}}},
		},
	}
	item := cardimport_service.BatchItem{
		Position: 1, SourceFileID: fileID, SourceRows: []int{2},
		SourceGroupKey: cardimport_service.NewSourceGroupKey(fileID, "", "test-sku"),
		VendorCode:     "test-sku", Payload: card,
	}
	checksum, err := cardimport_service.BatchChecksum(cardimport_service.PurposeTransfer, 1, []cardimport_service.BatchItem{item})
	if err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256([]byte("xlsx"))
	finalized := cardimport_service.FinalizedSession{
		Session: cardimport_service.Session{
			ID: sessionID, AuthorTelegramID: user.TelegramID,
			Purpose:  cardimport_service.PurposeTransfer,
			Status:   cardimport_service.SessionStatusCollecting,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Files: []cardimport_service.File{{
			ID: fileID, SessionID: sessionID, TelegramFileID: "test-file",
			TelegramFileUniqueID: "test-unique", TelegramMessageID: 1,
			OriginalFilename: "test.xlsx", DeclaredSize: 4, StoredSize: 4,
			SHA256: hex.EncodeToString(fileHash[:]), Status: cardimport_service.FileStatusValid,
			CreatedAt: now, UpdatedAt: now,
		}},
		Items: []cardimport_service.BatchItem{item}, GroupsCount: 1,
		Checksum: checksum, User: user,
		Command: cardimport_service.FinalizeCommand{
			SessionID: sessionID, ExpectedRevision: 1, IdempotencyKey: "test",
		},
		CommandDigest: cardimport_service.Digest(sha256.Sum256([]byte("test"))),
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
	if header.Checksum != checksum {
		t.Fatal("контрольная сумма изменилась при записи")
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
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM wb.card_import_sessions WHERE id = $1", sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("отмена транзакции оставила сессию в БД")
	}
}
