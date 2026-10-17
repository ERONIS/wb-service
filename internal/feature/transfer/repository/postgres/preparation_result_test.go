package transfer_postgres_repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type preparationRow func(...any) error

func (r preparationRow) Scan(dest ...any) error { return r(dest...) }

type preparationReplayTx struct {
	transaction.DBTX
	command                transfer.ApplyPreparationResultCommand
	phase, outcome, status string
	sameArtifact           bool
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
				if tx.command.Artifact != nil {
					*d[3].(*[]byte) = tx.command.ProposalRoot[:]
				}
			}
			return nil
		})
	case 3:
		return preparationRow(func(d ...any) error { *d[0].(*bool) = tx.sameArtifact; return nil })
	default:
		panic("неожиданный запрос")
	}
}

func TestPreparationReplayAfterCompletion(t *testing.T) {
	now := time.Now()
	metadata, _ := cardpipeline.DigestJSON("cardprepare-metadata:v1", json.RawMessage(`{}`))
	artifact := &transfer.PreparationArtifact{SubjectID: 1, SemanticDigest: transfer.Digest{1}, MetadataDigest: metadata,
		RequestPayload: []byte(`[]`), MetadataSnapshot: []byte(`{}`), LimitsObservedAt: now}
	artifact.ProposalRoot = cardpipeline.DigestProposal(artifact.SemanticDigest, metadata, artifact.RequestPayload)
	for _, tc := range []struct {
		name, phase, outcome, status string
		same, conflict               bool
	}{
		{"finished success", "finished", "succeeded", "succeeded", true, false},
		{"finished rejected", "finished", "rejected", "rejected", true, false},
		{"finished unresolved", "finished", "unresolved", "unresolved", true, false},
		{"artifact conflict", "finished", "succeeded", "succeeded", false, true},
		{"late new result", "finished", "cancelled", "running", false, true},
		{"initializing", "initializing", "running", "running", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := transfer.ApplyPreparationResultCommand{TransferID: 1, GroupTargetID: 2, Revision: 1,
				Status: transfer.PreparationResultSucceeded, OutcomeCode: "prepared", StartedAt: now, FinishedAt: now,
				Artifact: artifact, ProposalRoot: artifact.ProposalRoot}
			if tc.status == "rejected" || tc.status == "unresolved" {
				cmd.Status = transfer.PreparationResultStatus(tc.status)
				cmd.OutcomeCode = "invalid"
				cmd.Artifact = nil
				cmd.ProposalRoot = transfer.Digest{}
			}
			tx := &preparationReplayTx{command: cmd, phase: tc.phase, outcome: tc.outcome, status: tc.status, sameArtifact: tc.same}
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
