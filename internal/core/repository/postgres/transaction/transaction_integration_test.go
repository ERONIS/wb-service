package core_postgres_transaction

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresUnitOfWorkCommitRollbackAndStatementTimeout(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("создать тестовый пул PostgreSQL: %v", err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("получить соединение PostgreSQL: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE uow_test_rows (id integer PRIMARY KEY) ON COMMIT PRESERVE ROWS"); err != nil {
		t.Fatalf("создать временную таблицу: %v", err)
	}
	var baselineTimeout string
	if err := conn.QueryRow(ctx, "SHOW statement_timeout").Scan(&baselineTimeout); err != nil {
		t.Fatalf("прочитать исходный statement_timeout: %v", err)
	}

	uow := New(conn, pgx.TxOptions{})
	if err := uow.WithinTransaction(ctx, func(ctx context.Context, tx DBTX) error {
		_, err := tx.Exec(ctx, "INSERT INTO uow_test_rows (id) VALUES (1)")
		return err
	}); err != nil {
		t.Fatalf("зафиксировать транзакцию: %v", err)
	}

	rollbackReason := errors.New("отмена записи")
	err = uow.WithinTransaction(ctx, func(ctx context.Context, tx DBTX) error {
		if _, err := tx.Exec(ctx, "INSERT INTO uow_test_rows (id) VALUES (2)"); err != nil {
			return err
		}
		return rollbackReason
	})
	if !errors.Is(err, rollbackReason) {
		t.Fatalf("причина отката потеряна: %v", err)
	}
	var rowCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM uow_test_rows").Scan(&rowCount); err != nil {
		t.Fatalf("проверить строки после commit и rollback: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("после commit и rollback осталась %d строка, ожидалась 1", rowCount)
	}

	err = uow.WithinTransactionWithSettings(ctx, Settings{
		StatementTimeout: 50 * time.Millisecond,
	}, func(ctx context.Context, tx DBTX) error {
		var localTimeout string
		if err := tx.QueryRow(ctx, "SHOW statement_timeout").Scan(&localTimeout); err != nil {
			return err
		}
		if localTimeout != "50ms" {
			t.Errorf("локальный statement_timeout=%q, ожидалось 50ms", localTimeout)
		}
		_, err := tx.Exec(ctx, "SELECT pg_sleep(0.2)")
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("запрос не был отменён серверным statement_timeout: %v", err)
	}
	var restoredTimeout string
	if err := conn.QueryRow(ctx, "SHOW statement_timeout").Scan(&restoredTimeout); err != nil {
		t.Fatalf("прочитать таймаут после rollback: %v", err)
	}
	if restoredTimeout != baselineTimeout {
		t.Fatalf("statement_timeout после rollback=%q, ожидалось %q", restoredTimeout, baselineTimeout)
	}
	stats := uow.Stats()
	if stats.Attempts != 3 || stats.Commits != 1 || stats.Rollbacks != 2 {
		t.Fatalf("неверная статистика реальных транзакций: %+v", stats)
	}
}
