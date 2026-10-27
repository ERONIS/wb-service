package core_postgres_transaction

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const rollbackTimeout = 5 * time.Second

// ErrNestedTransaction сообщает о попытке открыть новую UoW внутри уже выполняемой транзакции.
var ErrNestedTransaction = errors.New("nested PostgreSQL transaction")

type transactionContextKey struct{}

// Settings задаёт ограничения отдельной транзакции.
type Settings struct {
	Timeout          time.Duration
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	TxOptions        *pgx.TxOptions
}

// Stats содержит накопленные показатели жизненного цикла транзакций.
type Stats struct {
	Attempts       int64
	Commits        int64
	Rollbacks      int64
	BeginErrors    int64
	CommitErrors   int64
	RollbackErrors int64
	Canceled       int64
	TotalDuration  time.Duration
}

type counters struct {
	attempts       atomic.Int64
	commits        atomic.Int64
	rollbacks      atomic.Int64
	beginErrors    atomic.Int64
	commitErrors   atomic.Int64
	rollbackErrors atomic.Int64
	canceled       atomic.Int64
	totalNanos     atomic.Int64
}

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
	stats   counters
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
	return u.WithinTransactionWithSettings(ctx, Settings{}, fn)
}

// WithinTransactionWithSettings выполняет callback в одной транзакции с локальными ограничениями PostgreSQL.
func (u *PGXUnitOfWork) WithinTransactionWithSettings(
	ctx context.Context,
	settings Settings,
	fn func(context.Context, DBTX) error,
) (err error) {
	if ctx == nil {
		return errors.New("PostgreSQL transaction context is nil")
	}
	if fn == nil {
		return errors.New("PostgreSQL transaction callback is nil")
	}
	if ctx.Value(transactionContextKey{}) != nil {
		return ErrNestedTransaction
	}
	if settings.Timeout < 0 || settings.StatementTimeout < 0 || settings.LockTimeout < 0 {
		return errors.New("PostgreSQL transaction timeouts must not be negative")
	}

	if settings.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, settings.Timeout)
		defer cancel()
	}
	u.stats.attempts.Add(1)
	started := time.Now()
	defer func() {
		u.stats.totalNanos.Add(time.Since(started).Nanoseconds())
		if ctx.Err() != nil {
			u.stats.canceled.Add(1)
		}
	}()

	options := u.options
	if settings.TxOptions != nil {
		options = *settings.TxOptions
	}

	prepared, err := beginPreparedTransaction(ctx, u.begin, options)
	if err != nil {
		u.stats.beginErrors.Add(1)
		return err
	}
	txCtx := context.WithValue(ctx, transactionContextKey{}, struct{}{})

	committed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			u.stats.rollbacks.Add(1)
			if prepared.Rollback() != nil {
				u.stats.rollbackErrors.Add(1)
			}
			panic(recovered)
		}
		if committed {
			return
		}

		u.stats.rollbacks.Add(1)
		rollbackErr := prepared.Rollback()
		if rollbackErr != nil {
			u.stats.rollbackErrors.Add(1)
			err = errors.Join(err, fmt.Errorf(
				"rollback PostgreSQL transaction: %w",
				rollbackErr,
			))
		}
	}()

	if err := prepared.Configure(txCtx, settings.StatementTimeout, settings.LockTimeout); err != nil {
		return err
	}
	if err := fn(txCtx, prepared.tx); err != nil {
		return fmt.Errorf("run PostgreSQL transaction: %w", err)
	}
	if err := prepared.Commit(ctx); err != nil {
		u.stats.commitErrors.Add(1)
		return fmt.Errorf("commit PostgreSQL transaction: %w", err)
	}

	committed = true
	u.stats.commits.Add(1)
	return nil
}

// Stats возвращает снимок накопленных показателей UoW.
func (u *PGXUnitOfWork) Stats() Stats {
	return Stats{
		Attempts:       u.stats.attempts.Load(),
		Commits:        u.stats.commits.Load(),
		Rollbacks:      u.stats.rollbacks.Load(),
		BeginErrors:    u.stats.beginErrors.Load(),
		CommitErrors:   u.stats.commitErrors.Load(),
		RollbackErrors: u.stats.rollbackErrors.Load(),
		Canceled:       u.stats.canceled.Load(),
		TotalDuration:  time.Duration(u.stats.totalNanos.Load()),
	}
}

// setLocalTimeout устанавливает ограничение только для текущей транзакции.
func setLocalTimeout(ctx context.Context, tx transaction, name string, timeout time.Duration) error {
	if timeout == 0 {
		return nil
	}
	milliseconds := timeout / time.Millisecond
	if timeout%time.Millisecond != 0 {
		milliseconds++
	}
	_, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", name, strconv.FormatInt(int64(milliseconds), 10)+"ms")
	return err
}
