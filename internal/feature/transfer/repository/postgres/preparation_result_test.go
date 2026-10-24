package transfer_postgres_repository

import (
	"context"
	"errors"
	"testing"
	"time"

	transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type preparationRow func(...any) error

func (r preparationRow) Scan(dest ...any) error { return r(dest...) }

type preparationReplayTx struct {
	transaction.DBTX
	command                preparation.ApplyPreparationResultCommand
	phase, outcome, status string
	reads, writes          int
}

func (tx *preparationReplayTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	tx.writes++
	return pgconn.CommandTag{}, errors.New("неожиданная запись при повторе")
}
func (tx *preparationReplayTx) QueryRow(context.Context, string, ...any) pgx.Row {
	tx.reads++
	switch tx.reads {
	case 1:
		return preparationRow(func(d ...any) error { *d[0].(*string) = tx.phase; *d[1].(*string) = tx.outcome; return nil })
	case 2:
		return preparationRow(func(d ...any) error {
			*d[0].(*string) = tx.status
			if tx.status != "running" {
				*d[1].(*int64) = tx.command.Revision
				*d[2].(**string) = &tx.command.OutcomeCode
			}
			return nil
		})
	default:
		panic("неожиданный запрос")
	}
}

func TestPreparationReplayAfterCompletion(t *testing.T) {
	now := time.Now()
	artifact := &preparation.PreparationArtifact{SubjectID: 1,
		RequestPayload: []byte(`[]`), MetadataSnapshot: []byte(`{}`), LimitsObservedAt: now}
	for _, tc := range []struct {
		name, phase, outcome, status string
		conflict                     bool
	}{
		{"finished success", "finished", "succeeded", "succeeded", false},
		{"finished rejected", "finished", "rejected", "rejected", false},
		{"finished unresolved", "finished", "unresolved", "unresolved", false},
		{"late new result", "finished", "cancelled", "running", true},
		{"initializing", "initializing", "running", "running", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := preparation.ApplyPreparationResultCommand{TransferID: 1, GroupTargetID: 2, Revision: 1,
				Status: transfer.PreparationResultSucceeded, OutcomeCode: "prepared", StartedAt: now, FinishedAt: now,
				Artifact: artifact}
			if tc.status == "rejected" || tc.status == "unresolved" {
				cmd.Status = preparation.PreparationResultStatus(tc.status)
				cmd.OutcomeCode = "invalid"
				cmd.Artifact = nil
			}
			tx := &preparationReplayTx{command: cmd, phase: tc.phase, outcome: tc.outcome, status: tc.status}
			err := (&Repository{}).ApplyPreparationResult(context.Background(), tx, cmd)
			if tc.conflict {
				if !errors.Is(err, transfer.ErrPreparationResultConflict) {
					t.Fatalf("ожидался конфликт: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tx.writes != 0 {
				t.Fatalf("повтор изменил данные: %d записей", tx.writes)
			}
		})
	}
}
