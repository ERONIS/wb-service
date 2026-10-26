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
		VerifiedAt:          time.Now(),
		Token:               token,
	}
	repository := New(&cabinetTestPool{Pool: connection})

	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := repository.UpsertVerified(ctx, tx, credential)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if rolledBack.ClientGeneration <= 0 {
		t.Fatal("новый кабинет получил недопустимую версию токена")
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

	commit := func() wbcabinet_service.Cabinet {
		t.Helper()
		transaction, err := connection.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cabinet, err := repository.UpsertVerified(ctx, transaction, credential)
		if err != nil {
			_ = transaction.Rollback(ctx)
			t.Fatal(err)
		}
		if err := transaction.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return cabinet
	}

	created := commit()
	if created.ClientGeneration <= rolledBack.ClientGeneration {
		t.Fatal("после отката новая запись должна получить следующую версию")
	}
	stored, err := repository.ListStored(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cabinet := range stored {
		if cabinet.ID == sellerID {
			found = true
			if cabinet.Token != credential.Token || cabinet.OwnerTelegramID != ownerID || cabinet.ClientGeneration != created.ClientGeneration {
				t.Fatal("восстановленный кабинет не соответствует сохранённым данным")
			}
			break
		}
	}
	if !found {
		t.Fatal("зафиксированный кабинет отсутствует в ListStored")
	}

	unchanged := commit()
	if unchanged.ClientGeneration != created.ClientGeneration {
		t.Fatal("повторная проверка того же токена изменила его версию")
	}
	credential.Token = token + "-rotated"
	rotated := commit()
	if rotated.ClientGeneration <= created.ClientGeneration {
		t.Fatal("смена токена не увеличила его версию")
	}
	unchanged = commit()
	if unchanged.ClientGeneration != rotated.ClientGeneration {
		t.Fatal("повторная проверка нового токена изменила его версию")
	}

	if err := repository.DeleteByOwner(ctx, ownerID, sellerID); err != nil {
		t.Fatal(err)
	}
	recreated := commit()
	if recreated.ClientGeneration <= rotated.ClientGeneration {
		t.Fatal("повторно созданный кабинет получил старую версию токена")
	}
}
