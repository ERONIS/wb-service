package users_service

import (
	"context"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type mutationTestDB struct{}

type mutationTestUOW struct {
	db    core_postgres_transaction.DBTX
	calls int
}

type mutationTestRepository struct {
	users      map[int64]domain.User
	db         core_postgres_transaction.DBTX
	beforeLock func()
	writes     int
}

func (*mutationTestDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("неожиданный прямой SQL-запрос")
}

func (*mutationTestDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("неожиданный прямой SQL-запрос")
}

func (*mutationTestDB) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("неожиданный прямой SQL-запрос")
}

func (*mutationTestDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("неожиданное копирование")
}

func (uow *mutationTestUOW) WithinTransaction(ctx context.Context, fn func(context.Context, core_postgres_transaction.DBTX) error) error {
	uow.calls++
	return fn(ctx, uow.db)
}

func (*mutationTestRepository) Create(context.Context, domain.User) (domain.User, error) {
	panic("неожиданное создание пользователя")
}

func (*mutationTestRepository) FindByTelegramID(context.Context, int64) (domain.User, error) {
	panic("проверка роли вышла из транзакции")
}

func (*mutationTestRepository) Update(context.Context, domain.User) (domain.User, error) {
	panic("изменение роли вышло из транзакции")
}

func (*mutationTestRepository) Delete(context.Context, int64) error {
	panic("удаление вышло из транзакции")
}

func (*mutationTestRepository) List(context.Context, int, int) ([]domain.User, error) {
	panic("неожиданный список пользователей")
}

func (repository *mutationTestRepository) LockUsersForMutation(
	_ context.Context,
	db core_postgres_transaction.DBTX,
	adminTelegramID int64,
	targetTelegramID int64,
) (map[int64]domain.User, error) {
	if db != repository.db {
		panic("проверка роли использовала другую транзакцию")
	}
	if repository.beforeLock != nil {
		repository.beforeLock()
	}
	users := make(map[int64]domain.User, 2)
	for _, id := range []int64{adminTelegramID, targetTelegramID} {
		if user, ok := repository.users[id]; ok {
			users[id] = user
		}
	}
	return users, nil
}

func (repository *mutationTestRepository) UpdateRoleWithin(
	_ context.Context,
	db core_postgres_transaction.DBTX,
	telegramID int64,
	role domain.UserRole,
) (domain.User, error) {
	if db != repository.db {
		panic("запись роли использовала другую транзакцию")
	}
	repository.writes++
	user := repository.users[telegramID]
	user.Role = role
	repository.users[telegramID] = user
	return user, nil
}

func (repository *mutationTestRepository) DeleteWithin(
	_ context.Context,
	db core_postgres_transaction.DBTX,
	telegramID int64,
) error {
	if db != repository.db {
		panic("удаление использовало другую транзакцию")
	}
	repository.writes++
	delete(repository.users, telegramID)
	return nil
}

func TestLockedMutationsRecheckCurrentRoles(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(map[int64]domain.User)
		invoke     func(*UsersService) error
		wantError  error
		wantWrites int
		wantNoTx   bool
	}{
		{
			name: "неверный идентификатор инициатора",
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 0, 2)
				return err
			},
			wantError: core_errors.ErrInvalidArgument,
			wantNoTx:  true,
		},
		{
			name: "несуществующий инициатор",
			mutate: func(users map[int64]domain.User) {
				delete(users, 1)
			},
			invoke: func(service *UsersService) error {
				return service.DeleteUser(context.Background(), 1, 2)
			},
			wantError: core_errors.ErrForbidden,
		},
		{
			name: "администратор лишился роли до блокировки",
			mutate: func(users map[int64]domain.User) {
				actor := users[1]
				actor.Role = domain.RoleUser
				users[1] = actor
			},
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 1, 2)
				return err
			},
			wantError: core_errors.ErrForbidden,
		},
		{
			name: "несуществующая цель",
			mutate: func(users map[int64]domain.User) {
				delete(users, 2)
			},
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 1, 2)
				return err
			},
			wantError: core_errors.ErrNotFound,
		},
		{
			name: "повторная выдача роли",
			mutate: func(users map[int64]domain.User) {
				target := users[2]
				target.Role = domain.RolePartner
				users[2] = target
			},
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 1, 2)
				return err
			},
			wantError: core_errors.ErrConflict,
		},
		{
			name: "повторная выдача роли администратора",
			mutate: func(users map[int64]domain.User) {
				target := users[2]
				target.Role = domain.RoleAdmin
				users[2] = target
			},
			invoke: func(service *UsersService) error {
				_, err := service.SetAdmin(context.Background(), 1, 2)
				return err
			},
			wantError: core_errors.ErrConflict,
		},
		{
			name: "цель получила роль администратора до изменения роли",
			mutate: func(users map[int64]domain.User) {
				target := users[2]
				target.Role = domain.RoleAdmin
				users[2] = target
			},
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 1, 2)
				return err
			},
			wantError: core_errors.ErrForbidden,
		},
		{
			name: "цель получила роль администратора до удаления",
			mutate: func(users map[int64]domain.User) {
				target := users[2]
				target.Role = domain.RoleAdmin
				users[2] = target
			},
			invoke: func(service *UsersService) error {
				return service.DeleteUser(context.Background(), 1, 2)
			},
			wantError: core_errors.ErrForbidden,
		},
		{
			name: "сохранение роли использует заблокированную транзакцию",
			invoke: func(service *UsersService) error {
				_, err := service.SetPartner(context.Background(), 1, 2)
				return err
			},
			wantWrites: 1,
		},
		{
			name: "удаление использует заблокированную транзакцию",
			invoke: func(service *UsersService) error {
				return service.DeleteUser(context.Background(), 1, 2)
			},
			wantWrites: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := &mutationTestDB{}
			uow := &mutationTestUOW{db: db}
			repository := &mutationTestRepository{
				users: map[int64]domain.User{
					1: {TelegramID: 1, Role: domain.RoleAdmin},
					2: {TelegramID: 2, Role: domain.RoleUser},
				},
				db: db,
			}
			repository.beforeLock = func() {
				if test.mutate != nil {
					test.mutate(repository.users)
				}
			}
			service := NewUsersService(repository, uow)
			err := test.invoke(service)
			if !core_errors.Is(err, test.wantError) {
				t.Fatalf("ошибка %v, ожидалась %v", err, test.wantError)
			}
			wantTransactions := 1
			if test.wantNoTx {
				wantTransactions = 0
			}
			if uow.calls != wantTransactions || repository.writes != test.wantWrites {
				t.Fatalf("транзакций %d, записей %d; ожидалось %d транзакций и %d записей", uow.calls, repository.writes, wantTransactions, test.wantWrites)
			}
		})
	}
}
