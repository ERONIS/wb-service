package wbcabinet_postgres_repository

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"

	"github.com/jackc/pgx/v5/pgxpool"
)

type cabinetTestPool struct {
	*pgxpool.Pool
}

func (pool *cabinetTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

// TestPostgresVerifiedCabinetRollbackAndRestore проверяет границу записи и чтение сохранённого токена.
func TestPostgresVerifiedCabinetRollbackAndRestore(t *testing.T) {
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

	ownerID := time.Now().UnixNano()
	if _, err := connection.Exec(ctx, `
		INSERT INTO wb.users (tg_id, full_name, role)
		VALUES ($1, 'Тестовый владелец', 'partner')`, ownerID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := connection.Exec(cleanupCtx, `DELETE FROM wb.api_cabinets WHERE owner_tg_id = $1`, ownerID); err != nil {
			t.Errorf("удалить тестовый кабинет: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `DELETE FROM wb.users WHERE tg_id = $1`, ownerID); err != nil {
			t.Errorf("удалить тестового владельца: %v", err)
		}
	}()

	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	sellerID := domain.SellerID(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]))
	token := "test-token-for-rollback"
	credential := wbcabinet_service.VerifiedCredential{
		SellerID:            sellerID,
		OwnerTelegramID:     ownerID,
		Name:                "Тестовый кабинет",
		ContentRead:         true,
		ContentWrite:        true,
		CredentialExpiresAt: time.Now().Add(time.Hour),
		ClientGeneration:    domain.NewClientGeneration(token),
		VerifiedAt:          time.Now(),
		Token:               token,
	}
	repository := New(&cabinetTestPool{Pool: connection})

	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertVerified(ctx, tx, credential); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := connection.QueryRow(ctx, `SELECT COUNT(*) FROM wb.api_cabinets WHERE seller_id = $1`, sellerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("отменённая транзакция оставила кабинет")
	}

	tx, err = connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertVerified(ctx, tx, credential); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListStored(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, cabinet := range stored {
		if cabinet.ID == sellerID {
			if cabinet.Token != credential.Token || cabinet.OwnerTelegramID != ownerID {
				t.Fatal("восстановленный кабинет не соответствует сохранённым данным")
			}
			return
		}
	}
	t.Fatal("зафиксированный кабинет отсутствует в ListStored")
}
