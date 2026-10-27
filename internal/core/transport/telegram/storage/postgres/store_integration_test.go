package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type menuTestPool struct {
	*pgxpool.Pool
}

func (pool *menuTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}

func TestPostgresMenuStoreSaveGetAndDelete(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pgxPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("создать тестовый пул PostgreSQL: %v", err)
	}
	t.Cleanup(pgxPool.Close)
	if err := pgxPool.Ping(ctx); err != nil {
		t.Fatalf("подключиться к PostgreSQL: %v", err)
	}

	chatID := time.Now().UnixNano()
	otherChatID := -chatID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pgxPool.Exec(cleanupCtx,
			"DELETE FROM wb.telegram_active_screens WHERE chat_id IN ($1, $2)",
			chatID, otherChatID,
		); err != nil {
			t.Errorf("удалить тестовые экраны: %v", err)
		}
	})
	store := New(&menuTestPool{Pool: pgxPool})

	if messageID, found, err := store.Get(ctx, chatID); err != nil || found || messageID != 0 {
		t.Fatalf("отсутствующий экран: message_id=%d found=%t err=%v", messageID, found, err)
	}
	if err := store.Save(ctx, chatID, 100); err != nil {
		t.Fatalf("сохранить первый экран: %v", err)
	}
	if err := store.Save(ctx, otherChatID, 200); err != nil {
		t.Fatalf("сохранить экран второго чата: %v", err)
	}
	if err := store.Save(ctx, chatID, 101); err != nil {
		t.Fatalf("обновить экран первого чата: %v", err)
	}
	if messageID, found, err := store.Get(ctx, chatID); err != nil || !found || messageID != 101 {
		t.Fatalf("обновлённый экран: message_id=%d found=%t err=%v", messageID, found, err)
	}
	if messageID, found, err := store.Get(ctx, otherChatID); err != nil || !found || messageID != 200 {
		t.Fatalf("экран второго чата изменился: message_id=%d found=%t err=%v", messageID, found, err)
	}

	if _, err := pgxPool.Exec(ctx, "DELETE FROM wb.telegram_active_screens WHERE chat_id = $1", chatID); err != nil {
		t.Fatalf("удалить экран первого чата: %v", err)
	}
	if messageID, found, err := store.Get(ctx, chatID); err != nil || found || messageID != 0 {
		t.Fatalf("удалённый экран: message_id=%d found=%t err=%v", messageID, found, err)
	}
	if messageID, found, err := store.Get(ctx, otherChatID); err != nil || !found || messageID != 200 {
		t.Fatalf("удаление затронуло другой чат: message_id=%d found=%t err=%v", messageID, found, err)
	}
}
