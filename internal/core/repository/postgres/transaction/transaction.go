package core_postgres_transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const rollbackTimeout = 5 * time.Second

// DBTX is the shared database surface accepted by business repositories.
// Implementations must not begin nested transactions.
type DBTX interface {
	Exec(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(
		ctx context.Context,
		tableName pgx.Identifier,
		columnNames []string,
		rowSrc pgx.CopyFromSource,
	) (int64, error)
}

type UnitOfWork interface {
	WithinTransaction(
		ctx context.Context,
		fn func(context.Context, DBTX) error,
	) error
}

type Beginner interface {
	BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error)
}

type transaction interface {
	DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type beginFunc func(context.Context, pgx.TxOptions) (transaction, error)

type PGXUnitOfWork struct {
	begin   beginFunc
	options pgx.TxOptions
}

func New(beginner Beginner, options pgx.TxOptions) *PGXUnitOfWork {
	if beginner == nil {
		panic("PostgreSQL transaction beginner is nil")
	}

	return newWithBegin(
		func(ctx context.Context, options pgx.TxOptions) (transaction, error) {
			return beginner.BeginTx(ctx, options)
		},
		options,
	)
}

func newWithBegin(begin beginFunc, options pgx.TxOptions) *PGXUnitOfWork {
	if begin == nil {
		panic("PostgreSQL transaction begin function is nil")
	}

	return &PGXUnitOfWork{
		begin:   begin,
		options: options,
	}
}

func (u *PGXUnitOfWork) WithinTransaction(
	ctx context.Context,
	fn func(context.Context, DBTX) error,
) (err error) {
	if ctx == nil {
		return errors.New("PostgreSQL transaction context is nil")
	}
	if fn == nil {
		return errors.New("PostgreSQL transaction callback is nil")
	}

	tx, err := u.begin(ctx, u.options)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL transaction: %w", err)
	}

	committed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback(tx, ctx)
			panic(recovered)
		}
		if committed {
			return
		}

		rollbackErr := rollback(tx, ctx)
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf(
				"rollback PostgreSQL transaction: %w",
				rollbackErr,
			))
		}
	}()

	if err := fn(ctx, tx); err != nil {
		return fmt.Errorf("run PostgreSQL transaction: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("PostgreSQL transaction context: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostgreSQL transaction: %w", err)
	}

	committed = true
	return nil
}

func rollback(tx transaction, parent context.Context) error {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(parent),
		rollbackTimeout,
	)
	defer cancel()

	err := tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrTxClosed) {
		return nil
	}

	return err
}
