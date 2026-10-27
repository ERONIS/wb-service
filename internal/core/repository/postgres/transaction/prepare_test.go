package core_postgres_transaction

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRollbackDetachedTreatsClosedTransactionAsComplete(t *testing.T) {
	err := rollbackDetached(context.Background(), func(context.Context) error {
		return pgx.ErrTxClosed
	})
	if err != nil {
		t.Fatalf("завершённая транзакция не требует повторного отката: %v", err)
	}
}
