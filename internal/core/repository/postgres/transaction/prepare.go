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

// beginPreparedTransaction открывает транзакцию для callback UoW.
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
	return rollbackDetached(p.parent, p.tx.Rollback)
}

// rollbackDetached выполняет откат в отдельном ограниченном контексте.
func rollbackDetached(parent context.Context, rollback func(context.Context) error) error {
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
