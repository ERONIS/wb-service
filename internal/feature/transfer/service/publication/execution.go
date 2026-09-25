package publication

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

type PublicationTerminalItem struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	OutcomeClass         ResultClass
	OutcomeCode          string
	NMID                 int64
}

type ApplyPublicationActionResultCommand struct {
	TransferID         TransferID
	ActionID           int64
	Items              []PublicationTerminalItem
	SkipMediaForGroups []int64
}

type ApplyPublicationMediaResultCommand struct {
	TransferID    TransferID
	GroupTargetID int64
	OutcomeClass  ResultClass
	OutcomeCode   string
}

func (command ApplyPublicationMediaResultCommand) Validate() error {
	if command.TransferID <= 0 || command.GroupTargetID <= 0 ||
		!command.OutcomeClass.IsValid() || command.OutcomeCode == "" ||
		strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		len(command.OutcomeCode) > 128 || command.OutcomeClass == ResultPartial {
		return errors.New("publication media result command is invalid")
	}
	return nil
}

type CorrectPublicationItemResultCommand struct {
	TransferID              TransferID
	ActionID                int64
	GroupTargetID           int64
	TransferItemTargetID    int64
	ExpectedItemRevision    int64
	ExpectedOutcomeClass    ResultClass
	ExpectedOutcomeCode     string
	ExpectedNMID            int64
	ExpectedAttentionClosed bool
	OutcomeClass            ResultClass
	OutcomeCode             string
	NMID                    int64
	CloseAttentionNoRetry   bool
}

func (command CorrectPublicationItemResultCommand) Validate() error {
	if command.TransferID <= 0 || command.ActionID <= 0 ||
		command.GroupTargetID <= 0 || command.TransferItemTargetID <= 0 ||
		command.ExpectedItemRevision < 0 ||
		(command.ExpectedOutcomeClass != ResultUnresolved &&
			command.ExpectedOutcomeClass != ResultInternalError) ||
		command.ExpectedOutcomeCode == "" ||
		strings.TrimSpace(command.ExpectedOutcomeCode) != command.ExpectedOutcomeCode ||
		len(command.ExpectedOutcomeCode) > 128 || command.ExpectedNMID < 0 ||
		!command.OutcomeClass.IsValid() || command.OutcomeCode == "" ||
		strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		len(command.OutcomeCode) > 128 || command.NMID < 0 {
		return errors.New("publication item correction command is invalid")
	}
	if command.CloseAttentionNoRetry {
		if command.ExpectedAttentionClosed {
			return errors.New("publication attention is already closed")
		}
		if command.OutcomeClass != command.ExpectedOutcomeClass ||
			command.OutcomeCode != command.ExpectedOutcomeCode ||
			command.NMID != command.ExpectedNMID {
			return errors.New("publication attention closure changes item result")
		}
		return nil
	}
	if command.OutcomeClass != ResultSuccess && command.OutcomeClass != ResultRejected {
		return errors.New("publication evidence correction result is invalid")
	}
	if (command.OutcomeClass == ResultSuccess && command.NMID <= 0) ||
		(command.OutcomeClass == ResultRejected && command.NMID != 0) {
		return errors.New("publication evidence correction remote identity is invalid")
	}
	return nil
}

func (command ApplyPublicationActionResultCommand) Validate() error {
	if command.TransferID <= 0 || command.ActionID <= 0 || len(command.Items) == 0 {
		return errors.New("publication action result command is invalid")
	}
	seenItems := make(map[int64]struct{}, len(command.Items))
	for index, item := range command.Items {
		if item.GroupTargetID <= 0 || item.TransferItemTargetID <= 0 ||
			!item.OutcomeClass.IsValid() || item.OutcomeCode == "" ||
			strings.TrimSpace(item.OutcomeCode) != item.OutcomeCode ||
			len(item.OutcomeCode) > 128 || item.NMID < 0 {
			return fmt.Errorf("publication terminal item at index %d is invalid", index)
		}
		if _, exists := seenItems[item.TransferItemTargetID]; exists {
			return errors.New("publication terminal item is duplicated")
		}
		seenItems[item.TransferItemTargetID] = struct{}{}
	}
	seenGroups := make(map[int64]struct{}, len(command.SkipMediaForGroups))
	for _, groupTargetID := range command.SkipMediaForGroups {
		if groupTargetID <= 0 {
			return errors.New("publication media group is invalid")
		}
		if _, exists := seenGroups[groupTargetID]; exists {
			return errors.New("publication media group is duplicated")
		}
		seenGroups[groupTargetID] = struct{}{}
	}
	return nil
}

type PublicationExecutionResultRepository interface {
	BeginPublicationAction(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
	) error

	MarkPublicationReconciling(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
	) error

	BeginPublicationMedia(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
	) error

	ApplyPublicationActionResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command ApplyPublicationActionResultCommand,
	) error

	ApplyPublicationMediaResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command ApplyPublicationMediaResultCommand,
	) error

	CorrectPublicationItemResult(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		command CorrectPublicationItemResultCommand,
	) error

	ResetPublicationPlanning(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID TransferID,
		planID int64,
	) error
}

type PublicationExecutionResultApplier struct {
	repository PublicationExecutionResultRepository
}

func NewPublicationExecutionResultApplier(
	repository PublicationExecutionResultRepository,
) *PublicationExecutionResultApplier {
	if repository == nil {
		panic("transfer publication execution result repository is nil")
	}
	return &PublicationExecutionResultApplier{repository: repository}
}

func (applier *PublicationExecutionResultApplier) BeginWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID TransferID,
) error {
	if ctx == nil || tx == nil || transferID <= 0 {
		return errors.New("begin publication action dependency is invalid")
	}
	return applier.repository.BeginPublicationAction(ctx, tx, transferID)
}

func (applier *PublicationExecutionResultApplier) MarkReconcilingWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID TransferID,
) error {
	if ctx == nil || tx == nil || transferID <= 0 {
		return errors.New("mark publication reconciling dependency is invalid")
	}
	return applier.repository.MarkPublicationReconciling(ctx, tx, transferID)
}

func (applier *PublicationExecutionResultApplier) BeginMediaWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID TransferID,
) error {
	if ctx == nil || tx == nil || transferID <= 0 {
		return errors.New("begin publication media dependency is invalid")
	}
	return applier.repository.BeginPublicationMedia(ctx, tx, transferID)
}

func (applier *PublicationExecutionResultApplier) ApplyWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ApplyPublicationActionResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("apply publication action result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	return applier.repository.ApplyPublicationActionResult(ctx, tx, command)
}

func (applier *PublicationExecutionResultApplier) ApplyMediaWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ApplyPublicationMediaResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("apply publication media result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	return applier.repository.ApplyPublicationMediaResult(ctx, tx, command)
}

func (applier *PublicationExecutionResultApplier) CorrectWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command CorrectPublicationItemResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("correct publication item result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	return applier.repository.CorrectPublicationItemResult(ctx, tx, command)
}

func (applier *PublicationExecutionResultApplier) ResetPlanningWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	transferID TransferID,
	planID int64,
) error {
	if ctx == nil || tx == nil || transferID <= 0 || planID <= 0 {
		return errors.New("reset publication planning dependency is invalid")
	}
	return applier.repository.ResetPublicationPlanning(ctx, tx, transferID, planID)
}
