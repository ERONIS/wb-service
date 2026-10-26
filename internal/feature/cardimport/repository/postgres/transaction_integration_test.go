package cardimport_postgres_repository_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_repository "github.com/ERONIS/wb-service/internal/feature/cardimport/repository/postgres"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationPool struct {
	*pgxpool.Pool
}

type cancelAfterExec struct {
	core_postgres_transaction.DBTX
	cancel context.CancelFunc
	limit  int
	calls  int
}

func (db *cancelAfterExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := db.DBTX.Exec(ctx, sql, args...)
	if err == nil {
		db.calls++
		if db.calls == db.limit {
			db.cancel()
		}
	}
	return tag, err
}

func (pool *integrationPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

func openIntegrationPool(t *testing.T) *integrationPool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("создать тестовый пул PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("подключиться к тестовому PostgreSQL: %v", err)
	}
	var table string
	if err := pool.QueryRow(ctx, "SELECT coalesce(to_regclass('wb.card_import_files')::text, '')").Scan(&table); err != nil || table == "" {
		t.Fatalf("миграции cardimport не применены: table=%q err=%v", table, err)
	}
	return &integrationPool{Pool: pool}
}

func createParsingFixture(t *testing.T, pool *integrationPool, fileCount int) (int64, cardimport_service.SessionID, []cardimport_service.FileID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authorID := time.Now().UnixNano()
	var sessionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO wb.card_import_sessions (author_telegram_id, purpose)
		VALUES ($1, 'transfer') RETURNING id
	`, authorID).Scan(&sessionID); err != nil {
		t.Fatalf("создать тестовую сессию: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, query := range []string{
			"DELETE FROM wb.card_import_issues WHERE session_id = $1",
			"DELETE FROM wb.card_import_items WHERE session_id = $1",
			"DELETE FROM wb.card_import_files WHERE session_id = $1",
			"DELETE FROM wb.card_import_sessions WHERE id = $1",
		} {
			if _, err := pool.Exec(cleanupCtx, query, sessionID); err != nil {
				t.Errorf("очистить тестовую сессию: %v", err)
			}
		}
	})

	digest := sha256.Sum256([]byte("x"))
	fileIDs := make([]cardimport_service.FileID, 0, fileCount)
	for i := range fileCount {
		var fileID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO wb.card_import_files (
				session_id, telegram_file_id, telegram_file_unique_id,
				telegram_message_id, original_filename, declared_size,
				stored_size, sha256, content, status
			) VALUES ($1, $2, $3, $4, 'test.xlsx', 1, 1, $5, $6, 'parsing')
			RETURNING id
		`, sessionID, fmt.Sprintf("file-%d-%d", sessionID, i),
			fmt.Sprintf("unique-%d-%d", sessionID, i), i+1, digest[:], []byte("x")).Scan(&fileID)
		if err != nil {
			t.Fatalf("создать тестовый файл: %v", err)
		}
		fileIDs = append(fileIDs, cardimport_service.FileID(fileID))
	}
	return authorID, cardimport_service.SessionID(sessionID), fileIDs
}

func TestPostgresSaveParsedFileRollsBackAfterCopyFailure(t *testing.T) {
	pool := openIntegrationPool(t)
	authorID, sessionID, files := createParsingFixture(t, pool, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		INSERT INTO wb.card_import_issues (session_id, file_id, severity, code, message)
		VALUES ($1, $2, 'warning', 'previous', 'Старое предупреждение')
	`, sessionID, files[0]); err != nil {
		t.Fatalf("создать прежнее предупреждение: %v", err)
	}

	repository := cardimport_repository.New(pool)
	uow := core_postgres_transaction.New(pool, pgx.TxOptions{})
	command := cardimport_service.ParseFileCommand{AuthorTelegramID: authorID, SessionID: sessionID, FileID: files[0]}
	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		_, err := repository.SaveParsedFile(ctx, tx, command, cardimport_service.AggregatedFile{
			FileID: files[0],
			Issues: []cardimport_service.ParseIssue{{Severity: cardimport_service.IssueSeverityError, Message: "Недопустимый код"}},
		})
		return err
	})
	if err == nil {
		t.Fatal("ошибка ограничения card_import_issues не получена")
	}

	var revision int64
	var status string
	var issueCode string
	if err := pool.QueryRow(ctx, "SELECT revision FROM wb.card_import_sessions WHERE id = $1", sessionID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM wb.card_import_files WHERE id = $1", files[0]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT code FROM wb.card_import_issues WHERE session_id = $1", sessionID).Scan(&issueCode); err != nil {
		t.Fatal(err)
	}
	if revision != 0 || status != "parsing" || issueCode != "previous" {
		t.Fatalf("часть записи пережила rollback: revision=%d status=%q issue=%q", revision, status, issueCode)
	}
}

func TestPostgresContinueRollsBackAfterCancellation(t *testing.T) {
	pool := openIntegrationPool(t)
	authorID, sessionID, files := createParsingFixture(t, pool, 1)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer setupCancel()
	if _, err := pool.Exec(setupCtx, "UPDATE wb.card_import_files SET status = 'invalid' WHERE id = $1", files[0]); err != nil {
		t.Fatalf("подготовить неверный файл: %v", err)
	}
	if _, err := pool.Exec(setupCtx, `
		INSERT INTO wb.card_import_issues (session_id, file_id, severity, code, message)
		VALUES ($1, $2, 'error', 'invalid_row', 'Неверная строка')
	`, sessionID, files[0]); err != nil {
		t.Fatalf("подготовить ошибку файла: %v", err)
	}

	repository := cardimport_repository.New(pool)
	uow := core_postgres_transaction.New(pool, pgx.TxOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		_, err := repository.ContinueAfterErrors(ctx, &cancelAfterExec{DBTX: tx, cancel: cancel, limit: 5}, cardimport_service.ContinueCommand{
			AuthorTelegramID: authorID,
			SessionID:        sessionID,
			ExpectedRevision: 0,
		})
		return err
	})
	if err == nil || ctx.Err() != context.Canceled {
		t.Fatalf("ожидалась отмена операции: %v", err)
	}

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer checkCancel()
	var revision int64
	var status string
	var issues int
	if err := pool.QueryRow(checkCtx, "SELECT revision FROM wb.card_import_sessions WHERE id = $1", sessionID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(checkCtx, "SELECT status FROM wb.card_import_files WHERE id = $1", files[0]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(checkCtx, "SELECT count(*) FROM wb.card_import_issues WHERE session_id = $1 AND severity = 'error'", sessionID).Scan(&issues); err != nil {
		t.Fatal(err)
	}
	if revision != 0 || status != "invalid" || issues != 1 {
		t.Fatalf("отмена оставила часть изменений: revision=%d status=%q issues=%d", revision, status, issues)
	}
}

func TestPostgresSaveParsedFilesSerializesSessionRevision(t *testing.T) {
	pool := openIntegrationPool(t)
	authorID, sessionID, files := createParsingFixture(t, pool, 2)
	repository := cardimport_repository.New(pool)
	uow := core_postgres_transaction.New(pool, pgx.TxOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := make(chan struct{})
	var workers sync.WaitGroup
	var failures [2]error
	for index, fileID := range files {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			command := cardimport_service.ParseFileCommand{AuthorTelegramID: authorID, SessionID: sessionID, FileID: fileID}
			failures[index] = uow.WithinTransaction(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
				_, err := repository.SaveParsedFile(ctx, tx, command, cardimport_service.AggregatedFile{FileID: fileID})
				return err
			})
		}()
	}
	close(start)
	workers.Wait()
	for index, err := range failures {
		if err != nil {
			t.Fatalf("обработка файла %d: %v", index, err)
		}
	}
	var revision int64
	if err := pool.QueryRow(ctx, "SELECT revision FROM wb.card_import_sessions WHERE id = $1", sessionID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	var validFiles int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM wb.card_import_files WHERE session_id = $1 AND status = 'valid'", sessionID).Scan(&validFiles); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || validFiles != 2 {
		t.Fatalf("параллельная обработка потеряла изменение: revision=%d valid=%d", revision, validFiles)
	}
}
