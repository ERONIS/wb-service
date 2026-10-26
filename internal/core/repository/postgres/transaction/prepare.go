package core_postgres_transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type preparedTransaction struct {
	tx     transaction
	parent context.Context
}

// PrepareTx открывает транзакцию и возвращает явные операции commit и rollback.
func PrepareTx(
	ctx context.Context,
	beginner Beginner,
) (pgx.Tx, func(context.Context) error, func() error, error) {
	return PrepareTxWithStatementTimeout(ctx, beginner, 0)
}

// PrepareTxWithStatementTimeout открывает транзакцию с локальным ограничением SQL-запросов.
func PrepareTxWithStatementTimeout(
	ctx context.Context,
	beginner Beginner,
	timeoutMs int,
	opts ...pgx.TxOptions,
) (pgx.Tx, func(context.Context) error, func() error, error) {
	if ctx == nil {
		return nil, nil, nil, errors.New("PostgreSQL transaction context is nil")
	}
	if beginner == nil {
		return nil, nil, nil, errors.New("PostgreSQL transaction beginner is nil")
	}
	if ctx.Value(transactionContextKey{}) != nil {
		return nil, nil, nil, ErrNestedTransaction
	}
	if timeoutMs < 0 || int64(timeoutMs) > int64(^uint64(0)>>1)/int64(time.Millisecond) {
		return nil, nil, nil, errors.New("PostgreSQL statement timeout is out of range")
	}
	if len(opts) > 1 {
		return nil, nil, nil, errors.New("multiple PostgreSQL transaction options")
	}

	var options pgx.TxOptions
	if len(opts) == 1 {
		options = opts[0]
	}
	prepared, err := beginPreparedTransaction(ctx, func(ctx context.Context, options pgx.TxOptions) (transaction, error) {
		return beginner.BeginTx(ctx, options)
	}, options)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := prepared.Configure(ctx, time.Duration(timeoutMs)*time.Millisecond, 0); err != nil {
		return nil, nil, nil, errors.Join(err, prepared.Rollback())
	}

	return prepared.tx.(pgx.Tx), prepared.Commit, prepared.Rollback, nil
}

// beginPreparedTransaction создаёт общий транзакционный исполнитель для двух публичных API.
func beginPreparedTransaction(
	ctx context.Context,
	begin beginFunc,
	options pgx.TxOptions,
) (*preparedTransaction, error) {
	tx, err := begin(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("begin PostgreSQL transaction: %w", err)
	}
	return &preparedTransaction{tx: tx, parent: ctx}, nil
}

// Configure задаёт ограничения только для текущей транзакции.
func (p *preparedTransaction) Configure(ctx context.Context, statementTimeout, lockTimeout time.Duration) error {
	if err := setLocalTimeout(ctx, p.tx, "statement_timeout", statementTimeout); err != nil {
		return fmt.Errorf("set PostgreSQL statement timeout: %w", err)
	}
	if err := setLocalTimeout(ctx, p.tx, "lock_timeout", lockTimeout); err != nil {
		return fmt.Errorf("set PostgreSQL lock timeout: %w", err)
	}
	return nil
}

// Commit завершает транзакцию контекстом вызывающей операции.
func (p *preparedTransaction) Commit(ctx context.Context) error {
	if ctx == nil {
		return errors.New("PostgreSQL commit context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("PostgreSQL transaction context: %w", err)
	}
	return p.tx.Commit(ctx)
}

// Rollback откатывает транзакцию с отдельным ограниченным бюджетом времени.
func (p *preparedTransaction) Rollback() error {
	return DeferRollback(p.parent, p.tx.Rollback)
}

// DeferRollback выполняет откат в отдельном ограниченном контексте.
func DeferRollback(parent context.Context, rollback func(context.Context) error) error {
	if parent == nil {
		return errors.New("PostgreSQL rollback context is nil")
	}
	if rollback == nil {
		return errors.New("PostgreSQL rollback function is nil")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), rollbackTimeout)
	defer cancel()

	err := rollback(ctx)
	if errors.Is(err, pgx.ErrTxClosed) {
		return nil
	}
	return err
}
