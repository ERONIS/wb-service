package core_postgres_transaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type testTransaction struct {
	pgx.Tx
	execCalls      [][]any
	execErr        error
	commitCalls    int
	rollbackCalls  int
	commitErr      error
	rollbackErr    error
	rollbackCtxErr error
}

func (tx *testTransaction) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls = append(tx.execCalls, args)
	return pgconn.CommandTag{}, tx.execErr
}

func (tx *testTransaction) Commit(context.Context) error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *testTransaction) Rollback(ctx context.Context) error {
	tx.rollbackCalls++
	tx.rollbackCtxErr = ctx.Err()
	return tx.rollbackErr
}

func TestWithinTransactionWithSettings(t *testing.T) {
	tx := &testTransaction{}
	options := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	uow := newWithBegin(func(_ context.Context, got pgx.TxOptions) (transaction, error) {
		if got.IsoLevel != options.IsoLevel {
			t.Fatalf("получен уровень изоляции %q", got.IsoLevel)
		}
		return tx, nil
	}, pgx.TxOptions{})

	err := uow.WithinTransactionWithSettings(context.Background(), Settings{
		TxOptions:        &options,
		StatementTimeout: 1500 * time.Microsecond,
		LockTimeout:      time.Second,
	}, func(_ context.Context, db DBTX) error {
		if db != tx {
			t.Fatal("callback получил другой исполнитель")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("транзакция завершилась с ошибкой: %v", err)
	}
	if len(tx.execCalls) != 2 || tx.execCalls[0][0] != "statement_timeout" || tx.execCalls[0][1] != "2ms" || tx.execCalls[1][0] != "lock_timeout" || tx.execCalls[1][1] != "1000ms" {
		t.Fatalf("неверные локальные ограничения: %#v", tx.execCalls)
	}
	if tx.commitCalls != 1 || tx.rollbackCalls != 0 {
		t.Fatalf("commit=%d rollback=%d", tx.commitCalls, tx.rollbackCalls)
	}
	stats := uow.Stats()
	if stats.Attempts != 1 || stats.Commits != 1 || stats.Rollbacks != 0 || stats.TotalDuration <= 0 {
		t.Fatalf("неверная статистика: %+v", stats)
	}
}

func TestWithinTransactionRejectsNestedCall(t *testing.T) {
	tx := &testTransaction{}
	beginCalls := 0
	uow := newWithBegin(func(context.Context, pgx.TxOptions) (transaction, error) {
		beginCalls++
		return tx, nil
	}, pgx.TxOptions{})

	err := uow.WithinTransaction(context.Background(), func(ctx context.Context, _ DBTX) error {
		return uow.WithinTransaction(ctx, func(context.Context, DBTX) error { return nil })
	})
	if !errors.Is(err, ErrNestedTransaction) {
		t.Fatalf("ожидалась ошибка вложенной транзакции, получено: %v", err)
	}
	if beginCalls != 1 || tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("begin=%d commit=%d rollback=%d", beginCalls, tx.commitCalls, tx.rollbackCalls)
	}
}

func TestWithinTransactionRollbackUsesIndependentContext(t *testing.T) {
	tx := &testTransaction{}
	uow := newWithBegin(func(context.Context, pgx.TxOptions) (transaction, error) { return tx, nil }, pgx.TxOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	err := uow.WithinTransaction(ctx, func(context.Context, DBTX) error {
		cancel()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась отмена контекста, получено: %v", err)
	}
	if tx.rollbackCalls != 1 || tx.rollbackCtxErr != nil {
		t.Fatalf("rollback вызван %d раз с отменённым контекстом", tx.rollbackCalls)
	}
	if uow.Stats().Canceled != 1 {
		t.Fatalf("отмена не отражена в статистике: %+v", uow.Stats())
	}
}

func TestWithinTransactionRollsBackAndRepanics(t *testing.T) {
	tx := &testTransaction{}
	uow := newWithBegin(func(context.Context, pgx.TxOptions) (transaction, error) {
		return tx, nil
	}, pgx.TxOptions{})
	defer func() {
		if recovered := recover(); recovered != "callback panic" {
			t.Fatalf("panic не передан вызывающему коду: %v", recovered)
		}
		if tx.commitCalls != 0 || tx.rollbackCalls != 1 || tx.rollbackCtxErr != nil {
			t.Fatalf("неверное завершение после panic: commit=%d rollback=%d context=%v", tx.commitCalls, tx.rollbackCalls, tx.rollbackCtxErr)
		}
		stats := uow.Stats()
		if stats.Attempts != 1 || stats.Commits != 0 || stats.Rollbacks != 1 {
			t.Fatalf("неверная статистика после panic: %+v", stats)
		}
	}()

	_ = uow.WithinTransaction(context.Background(), func(context.Context, DBTX) error {
		panic("callback panic")
	})
}

func TestWithinTransactionPreservesCommitAndRollbackErrors(t *testing.T) {
	commitErr := errors.New("результат commit неизвестен")
	rollbackErr := errors.New("rollback не удался")
	tx := &testTransaction{commitErr: commitErr, rollbackErr: rollbackErr}
	uow := newWithBegin(func(context.Context, pgx.TxOptions) (transaction, error) { return tx, nil }, pgx.TxOptions{})

	err := uow.WithinTransaction(context.Background(), func(context.Context, DBTX) error { return nil })
	if !errors.Is(err, commitErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("ошибки commit/rollback потеряны: %v", err)
	}
	stats := uow.Stats()
	if stats.CommitErrors != 1 || stats.Rollbacks != 1 || stats.RollbackErrors != 1 || stats.Commits != 0 {
		t.Fatalf("неверная статистика: %+v", stats)
	}
}

func TestWithinTransactionRejectsNegativeSettingsBeforeBegin(t *testing.T) {
	called := false
	uow := newWithBegin(func(context.Context, pgx.TxOptions) (transaction, error) {
		called = true
		return &testTransaction{}, nil
	}, pgx.TxOptions{})

	err := uow.WithinTransactionWithSettings(context.Background(), Settings{LockTimeout: -time.Second}, func(context.Context, DBTX) error { return nil })
	if err == nil || called {
		t.Fatalf("неверная конфигурация открыла транзакцию: err=%v called=%v", err, called)
	}
}
