package preparation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

var ErrPreparationResultConflict = core_errors.ErrConflict

type PreparationResultStatus string

const (
	PreparationResultSucceeded  PreparationResultStatus = "succeeded"
	PreparationResultRejected   PreparationResultStatus = "rejected"
	PreparationResultUnresolved PreparationResultStatus = "unresolved"
)

type PreparationArtifact struct {
	SubjectID        int64
	SemanticDigest   Digest
	MetadataDigest   Digest
	ProposalRoot     Digest
	RequestPayload   []byte
	MetadataSnapshot []byte
	LimitsFree       int64
	LimitsPaid       int64
	LimitsObservedAt time.Time
}

// ApplyPreparationResultCommand содержит параметры применения результата подготовки целевой группы.
type ApplyPreparationResultCommand struct {
	TransferID    TransferID
	GroupTargetID int64
	Status        PreparationResultStatus
	OutcomeCode   string
	ItemPosition  int
	Field         string
	StartedAt     time.Time
	FinishedAt    time.Time
	ProposalRoot  Digest
	Artifact      *PreparationArtifact
	Revision      int64
}

// Validate проверяет согласованность команды применения результата подготовки и её диагностических данных.
func (command ApplyPreparationResultCommand) Validate() error {
	if command.TransferID <= 0 || command.GroupTargetID <= 0 ||
		command.Revision != 1 || strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		command.OutcomeCode == "" || len(command.OutcomeCode) > 128 ||
		command.StartedAt.IsZero() || command.FinishedAt.IsZero() ||
		command.FinishedAt.Before(command.StartedAt) {
		return errors.New("apply transfer preparation result command is invalid")
	}
	if command.ItemPosition < 0 || len(command.Field) > 128 ||
		strings.TrimSpace(command.Field) != command.Field {
		return errors.New("apply transfer preparation result diagnostics are invalid")
	}
	switch command.Status {
	case PreparationResultSucceeded:
		if command.OutcomeCode != "prepared" || command.ProposalRoot == (Digest{}) ||
			command.Artifact == nil || command.ItemPosition != 0 || command.Field != "" {
			return errors.New("successful transfer preparation result is invalid")
		}
		artifact := command.Artifact
		metadataDigest, err := cardpipeline.DigestJSON("cardprepare-metadata:v1", json.RawMessage(artifact.MetadataSnapshot))
		if err != nil || artifact.SubjectID <= 0 || artifact.SemanticDigest == (Digest{}) ||
			artifact.MetadataDigest != metadataDigest || artifact.ProposalRoot != command.ProposalRoot ||
			!json.Valid(artifact.RequestPayload) || artifact.LimitsObservedAt.IsZero() ||
			artifact.LimitsFree < 0 || artifact.LimitsPaid < 0 ||
			cardpipeline.DigestProposal(artifact.SemanticDigest, artifact.MetadataDigest, artifact.RequestPayload) != command.ProposalRoot {
			return errors.New("transfer preparation artifact integrity is invalid")
		}
	case PreparationResultRejected, PreparationResultUnresolved:
		if command.ProposalRoot != (Digest{}) || command.Artifact != nil {
			return errors.New("unsuccessful transfer preparation result has proposal root")
		}
	default:
		return errors.New("transfer preparation result status is invalid")
	}
	return nil
}

type PreparationResultApplier struct {
	repository PreparationResultRepository
	uow        core_postgres_transaction.UnitOfWork
}

func NewPreparationResultApplier(
	repository PreparationResultRepository,
	uow core_postgres_transaction.UnitOfWork,
) *PreparationResultApplier {
	if repository == nil {
		panic("transfer preparation result repository is nil")
	}
	if uow == nil {
		panic("transfer preparation result unit of work is nil")
	}
	return &PreparationResultApplier{
		repository: repository,
		uow:        uow,
	}
}

func (applier *PreparationResultApplier) Apply(
	ctx context.Context,
	command ApplyPreparationResultCommand,
) error {
	if ctx == nil {
		return errors.New("apply transfer preparation result: context is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	if err := applier.uow.WithinTransaction(
		ctx,
		func(ctx context.Context, tx core_postgres_transaction.DBTX) error {
			return applier.repository.ApplyPreparationResult(ctx, tx, command)
		},
	); err != nil {
		return fmt.Errorf("apply transfer preparation result: %w", err)
	}
	return nil
}

// ApplyTx применяет результат подготовки transfer внутри внешней транзакции.
func (applier *PreparationResultApplier) ApplyTx(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ApplyPreparationResultCommand,
) error {
	if ctx == nil {
		return errors.New("apply transfer preparation result in tx: context is nil")
	}
	if tx == nil {
		return errors.New("apply transfer preparation result in tx: DBTX is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	return applier.repository.ApplyPreparationResult(ctx, tx, command)
}
