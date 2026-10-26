package users_postgres_repository_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	users_postgres_repository "github.com/ERONIS/wb-service/internal/feature/users/repository/postgres"
	users_service "github.com/ERONIS/wb-service/internal/feature/users/service"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lockedUsersTestPool struct {
	*pgxpool.Pool
}

func (*lockedUsersTestPool) OperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}

func TestPostgresLockedMutationsSeeConcurrentRoleChanges(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	pool := &lockedUsersTestPool{Pool: connection}

	var table *string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('wb.users')::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table == nil {
		t.Fatal("таблица wb.users отсутствует")
	}

	adminID := time.Now().UnixNano()
	targetID := adminID + 1
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM wb.users WHERE tg_id = ANY($1)", []int64{adminID, targetID}); err != nil {
			t.Errorf("очистить тестовых пользователей: %v", err)
		}
	})
	for _, user := range []struct {
		id   int64
		role domain.UserRole
	}{
		{id: adminID, role: domain.RoleAdmin},
		{id: targetID, role: domain.RoleUser},
	} {
		if _, err := pool.Exec(ctx,
			"INSERT INTO wb.users (tg_id, full_name, role) VALUES ($1, $2, $3)",
			user.id, "Тестовый Пользователь", user.role,
		); err != nil {
			t.Fatal(err)
		}
	}
	service := users_service.NewUsersService(
		users_postgres_repository.New(pool),
		core_postgres_transaction.New(pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}),
	)
	for _, test := range []struct {
		name           string
		changedID      int64
		changedRole    domain.UserRole
		deleteUser     bool
		wantTargetRole domain.UserRole
	}{
		{name: "цель стала администратором перед изменением роли", changedID: targetID, changedRole: domain.RoleAdmin, wantTargetRole: domain.RoleAdmin},
		{name: "цель стала администратором перед удалением", changedID: targetID, changedRole: domain.RoleAdmin, deleteUser: true, wantTargetRole: domain.RoleAdmin},
		{name: "инициатор лишился роли перед изменением", changedID: adminID, changedRole: domain.RoleUser, wantTargetRole: domain.RoleUser},
		{name: "инициатор лишился роли перед удалением", changedID: adminID, changedRole: domain.RoleUser, deleteUser: true, wantTargetRole: domain.RoleUser},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, "UPDATE wb.users SET role = 'admin' WHERE tg_id = $1", adminID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "UPDATE wb.users SET role = 'user' WHERE tg_id = $1", targetID); err != nil {
				t.Fatal(err)
			}

			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err := blocker.Exec(ctx, "UPDATE wb.users SET role = $1 WHERE tg_id = $2", test.changedRole, test.changedID); err != nil {
				t.Fatal(err)
			}

			result := make(chan error, 1)
			go func() {
				if test.deleteUser {
					result <- service.DeleteUser(ctx, adminID, targetID)
					return
				}
				_, err := service.SetPartner(ctx, adminID, targetID)
				result <- err
			}()
			select {
			case err := <-result:
				t.Fatalf("операция завершилась до освобождения блокировки: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, core_errors.ErrForbidden) {
					t.Fatalf("ожидался запрет после смены роли, получено %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("ожидание завершения операции: %v", ctx.Err())
			}

			var role domain.UserRole
			if err := pool.QueryRow(ctx, "SELECT role FROM wb.users WHERE tg_id = $1", targetID).Scan(&role); err != nil {
				t.Fatalf("целевой пользователь удалён или недоступен: %v", err)
			}
			if role != test.wantTargetRole {
				t.Fatalf("роль цели %s, ожидалась %s", role, test.wantTargetRole)
			}
		})
	}
}
