package transfer_postgres_publication

import (
	"context"
	"errors"
	"strings"
	"testing"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type activePublicationRow struct {
	id  transfer_model.TransferID
	err error
}

type activePublicationTx struct {
	core_postgres_transaction.DBTX
	query  string
	rowErr error
	writes int
}

func (row activePublicationRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	*dest[0].(*transfer_model.TransferID) = row.id
	return nil
}

func (tx *activePublicationTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.query = query
	return activePublicationRow{id: args[0].(transfer_model.TransferID), err: tx.rowErr}
}

func (tx *activePublicationTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	tx.writes++
	return pgconn.CommandTag{}, errors.New("unexpected transfer write")
}

func TestEnsureActivePublicationLocksWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name      string
		rowErr    error
		wantError error
	}{
		{name: "active"},
		{name: "inactive", rowErr: pgx.ErrNoRows, wantError: transfer_preparation.ErrPreparationResultConflict},
		{name: "database error", rowErr: errors.New("database unavailable"), wantError: errors.New("database unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &activePublicationTx{rowErr: test.rowErr}
			err := (&PublicationRepository{}).EnsureActivePublication(context.Background(), tx, 12)
			if test.wantError == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) &&
				(err == nil || !strings.Contains(err.Error(), test.wantError.Error())) {
				t.Fatalf("unexpected error: %v", err)
			}
			if tx.writes != 0 {
				t.Fatalf("active publication guard wrote to transfer: %d", tx.writes)
			}
			for _, condition := range []string{"phase = 'active'", "outcome = 'running'", "FOR UPDATE"} {
				if !strings.Contains(tx.query, condition) {
					t.Fatalf("active publication guard omitted %q", condition)
				}
			}
		})
	}
}
