package publication

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type PublicationTerminalItem struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	OutcomeClass         transfer_model.ResultClass
	OutcomeCode          string
	NMID                 int64
}

type ApplyPublicationActionResultCommand struct {
	TransferID         transfer_model.TransferID
	ActionID           int64
	Items              []PublicationTerminalItem
	SkipMediaForGroups []int64
}

type ApplyPublicationMediaResultCommand struct {
	TransferID    transfer_model.TransferID
	PlanID        int64
	ActionID      int64
	GroupTargetID int64
	OutcomeClass  transfer_model.ResultClass
	OutcomeCode   string
}

type CorrectPublicationItemResultCommand struct {
	TransferID              transfer_model.TransferID
	ActionID                int64
	GroupTargetID           int64
	TransferItemTargetID    int64
	ExpectedItemRevision    int64
	ExpectedOutcomeClass    transfer_model.ResultClass
	ExpectedOutcomeCode     string
	ExpectedNMID            int64
	ExpectedAttentionClosed bool
	OutcomeClass            transfer_model.ResultClass
	OutcomeCode             string
	NMID                    int64
	CloseAttentionNoRetry   bool
}

type PublicationExecutionResultRepository interface {
	EnsureActivePublication(
		ctx context.Context,
		tx core_postgres_transaction.DBTX,
		transferID transfer_model.TransferID,
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
		transferID transfer_model.TransferID,
		planID int64,
	) error
}

func (command ApplyPublicationMediaResultCommand) Validate() error {
	if command.TransferID <= 0 || command.PlanID <= 0 ||
		command.ActionID <= 0 || command.GroupTargetID <= 0 ||
		!command.OutcomeClass.IsValid() || command.OutcomeCode == "" ||
		strings.TrimSpace(command.OutcomeCode) != command.OutcomeCode ||
		len(command.OutcomeCode) > 128 || command.OutcomeClass == transfer_model.ResultPartial {
		return errors.New("publication media result command is invalid")
	}
	return nil
}

func (command CorrectPublicationItemResultCommand) Validate() error {
	if command.TransferID <= 0 || command.ActionID <= 0 ||
		command.GroupTargetID <= 0 || command.TransferItemTargetID <= 0 ||
		command.ExpectedItemRevision < 0 ||
		(command.ExpectedOutcomeClass != transfer_model.ResultUnresolved &&
			command.ExpectedOutcomeClass != transfer_model.ResultInternalError) ||
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
	if command.OutcomeClass != transfer_model.ResultSuccess && command.OutcomeClass != transfer_model.ResultRejected {
		return errors.New("publication evidence correction result is invalid")
	}
	if (command.OutcomeClass == transfer_model.ResultSuccess && command.NMID <= 0) ||
		(command.OutcomeClass == transfer_model.ResultRejected && command.NMID != 0) {
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
