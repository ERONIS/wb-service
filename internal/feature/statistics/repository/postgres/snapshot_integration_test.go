package statistics_postgres_repository

import (
	"context"
	"os"
	"testing"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"

	"github.com/jackc/pgx/v5/pgxpool"
)

type snapshotTestPool struct {
	*pgxpool.Pool
}

func (pool *snapshotTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

// TestPostgresStatisticsSnapshot проверяет единый снимок при конкурентном изменении данных.
func TestPostgresStatisticsSnapshot(t *testing.T) {
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
	if err := connection.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	telegramID := time.Now().UnixNano()
	if _, err := connection.Exec(ctx, `
		INSERT INTO wb.users (tg_id, full_name, role)
		VALUES ($1, 'Начальное имя', 'user')`, telegramID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := connection.Exec(cleanupCtx, `DELETE FROM wb.users WHERE tg_id = $1`, telegramID); err != nil {
			t.Errorf("удалить тестового пользователя: %v", err)
		}
	}()

	repository := New(&snapshotTestPool{Pool: connection})
	err = repository.withinSnapshot(ctx, func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
		var before, after string
		if err := tx.QueryRow(ctx, `SELECT full_name FROM wb.users WHERE tg_id = $1`, telegramID).Scan(&before); err != nil {
			return err
		}
		if _, err := connection.Exec(ctx, `
			UPDATE wb.users SET full_name = 'Новое имя' WHERE tg_id = $1`, telegramID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT full_name FROM wb.users WHERE tg_id = $1`, telegramID).Scan(&after); err != nil {
			return err
		}
		if before != "Начальное имя" || after != before {
			t.Errorf("чтения получили разные снимки: before=%q after=%q", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
