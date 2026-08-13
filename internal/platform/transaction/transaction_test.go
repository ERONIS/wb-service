package platform_transaction

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errTest = errors.New("test error")

type fakeTransaction struct {
	commitErr     error
	rollbackErr   error
	commitCalls   int
	rollbackCalls int
}

func (t *fakeTransaction) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (t *fakeTransaction) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, nil
}

func (t *fakeTransaction) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return nil
}

func (t *fakeTransaction) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, nil
}

func (t *fakeTransaction) Commit(context.Context) error {
	t.commitCalls++
	return t.commitErr
}

func (t *fakeTransaction) Rollback(context.Context) error {
	t.rollbackCalls++
	return t.rollbackErr
}

func newTestUnitOfWork(tx *fakeTransaction) *PGXUnitOfWork {
	return newWithBegin(
		func(context.Context, pgx.TxOptions) (transaction, error) {
			return tx, nil
		},
		pgx.TxOptions{},
	)
}

func TestWithinTransactionCommitsSuccessfulCallback(t *testing.T) {
	t.Parallel()

	tx := &fakeTransaction{}
	uow := newTestUnitOfWork(tx)

	err := uow.WithinTransaction(
		context.Background(),
		func(context.Context, DBTX) error { return nil },
	)
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}
	if tx.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want 1", tx.commitCalls)
	}
	if tx.rollbackCalls != 0 {
		t.Fatalf("rollback calls = %d, want 0", tx.rollbackCalls)
	}
}

func TestWithinTransactionRollsBackCallbackError(t *testing.T) {
	t.Parallel()

	tx := &fakeTransaction{}
	uow := newTestUnitOfWork(tx)

	err := uow.WithinTransaction(
		context.Background(),
		func(context.Context, DBTX) error { return errTest },
	)
	if !errors.Is(err, errTest) {
		t.Fatalf("WithinTransaction() error = %v, want %v", err, errTest)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0", tx.commitCalls)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", tx.rollbackCalls)
	}
}

func TestWithinTransactionRollsBackCommitError(t *testing.T) {
	t.Parallel()

	tx := &fakeTransaction{commitErr: errTest}
	uow := newTestUnitOfWork(tx)

	err := uow.WithinTransaction(
		context.Background(),
		func(context.Context, DBTX) error { return nil },
	)
	if !errors.Is(err, errTest) {
		t.Fatalf("WithinTransaction() error = %v, want %v", err, errTest)
	}
	if tx.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want 1", tx.commitCalls)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", tx.rollbackCalls)
	}
}

func TestWithinTransactionRollsBackCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	tx := &fakeTransaction{}
	uow := newTestUnitOfWork(tx)

	err := uow.WithinTransaction(
		ctx,
		func(context.Context, DBTX) error {
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WithinTransaction() error = %v, want context canceled", err)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0", tx.commitCalls)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", tx.rollbackCalls)
	}
}

func TestWithinTransactionRollsBackAndRepanics(t *testing.T) {
	t.Parallel()

	tx := &fakeTransaction{}
	uow := newTestUnitOfWork(tx)

	defer func() {
		if recovered := recover(); recovered != "boom" {
			t.Fatalf("recovered = %v, want boom", recovered)
		}
		if tx.commitCalls != 0 {
			t.Fatalf("commit calls = %d, want 0", tx.commitCalls)
		}
		if tx.rollbackCalls != 1 {
			t.Fatalf("rollback calls = %d, want 1", tx.rollbackCalls)
		}
	}()

	_ = uow.WithinTransaction(
		context.Background(),
		func(context.Context, DBTX) error { panic("boom") },
	)
}
