package transfer_service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const MaxPublicationPlanningPageSize = 1000

type PublicationPlanningMember struct {
	TransferItemID       int64
	TransferItemTargetID int64
	Position             int
	VendorCode           string
	Media                cardimport_service.ParsedMedia
}

type PublicationPlanningGroup struct {
	GroupTargetID      int64
	SourceGroupID      int64
	TargetID           int64
	CabinetID          CabinetID
	PreparationGroupID int64
	ProposalRoot       Digest
	Members            []PublicationPlanningMember
}

type PublicationPlanningSource struct {
	TransferID TransferID
	Groups     []PublicationPlanningGroup
}

func (source PublicationPlanningSource) Validate() error {
	if source.TransferID <= 0 {
		return invalidTransfer("publication planning source transfer ID is invalid")
	}
	seenGroups := make(map[int64]struct{}, len(source.Groups))
	seenItemTargets := make(map[int64]struct{})
	for groupIndex, group := range source.Groups {
		if group.GroupTargetID <= 0 || group.SourceGroupID <= 0 ||
			group.TargetID <= 0 || group.PreparationGroupID <= 0 ||
			group.ProposalRoot == (Digest{}) || len(group.Members) == 0 ||
			strings.TrimSpace(string(group.CabinetID)) != string(group.CabinetID) ||
			group.CabinetID == "" {
			return fmt.Errorf("publication planning group at index %d is invalid", groupIndex)
		}
		if _, exists := seenGroups[group.GroupTargetID]; exists {
			return errors.New("publication planning group is duplicated")
		}
		seenGroups[group.GroupTargetID] = struct{}{}
		for memberIndex, member := range group.Members {
			if member.TransferItemID <= 0 || member.TransferItemTargetID <= 0 ||
				member.Position <= 0 || member.VendorCode == "" ||
				strings.TrimSpace(member.VendorCode) != member.VendorCode ||
				(memberIndex > 0 && member.Position <= group.Members[memberIndex-1].Position) {
				return fmt.Errorf(
					"publication planning member at index %d/%d is invalid",
					groupIndex,
					memberIndex,
				)
			}
			if _, exists := seenItemTargets[member.TransferItemTargetID]; exists {
				return errors.New("publication planning item-target is duplicated")
			}
			seenItemTargets[member.TransferItemTargetID] = struct{}{}
		}
	}
	return nil
}

func (service *Service) ListAwaitingAuthorization(
	ctx context.Context,
	afterID TransferID,
	limit int,
) ([]Transfer, error) {
	if ctx == nil {
		return nil, errors.New("list awaiting-authorization transfers: context is nil")
	}
	if afterID < 0 || limit <= 0 || limit > MaxPublicationPlanningPageSize {
		return nil, core_errors.ErrInvalidArgument
	}
	return service.repository.ListAwaitingAuthorization(ctx, afterID, limit)
}

func (service *Service) PublicationTargets(
	ctx context.Context,
) ([]MutationTarget, error) {
	if ctx == nil {
		return nil, errors.New("load publication targets: context is nil")
	}
	snapshot, err := service.targetRegistry.MutationSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("load publication target snapshot: %w", err)
	}
	return append([]MutationTarget(nil), snapshot.Targets...), nil
}

func (service *Service) LoadPublicationPlanningSource(
	ctx context.Context,
	transferID TransferID,
) (PublicationPlanningSource, error) {
	if ctx == nil {
		return PublicationPlanningSource{}, errors.New(
			"load publication planning source: context is nil",
		)
	}
	if transferID <= 0 {
		return PublicationPlanningSource{}, core_errors.ErrInvalidArgument
	}
	source, err := service.repository.LoadPublicationPlanningSource(ctx, transferID)
	if err != nil {
		return PublicationPlanningSource{}, err
	}
	if err := source.Validate(); err != nil {
		return PublicationPlanningSource{}, fmt.Errorf(
			"validate publication planning source: %w",
			err,
		)
	}
	return source, nil
}

type PublicationProjectionStatus string

const (
	PublicationProjectionRunning  PublicationProjectionStatus = "running"
	PublicationProjectionSkipped  PublicationProjectionStatus = "skipped"
	PublicationProjectionRejected PublicationProjectionStatus = "rejected"
)

type PublicationPlannedGroup struct {
	GroupTargetID  int64
	Status         PublicationProjectionStatus
	OutcomeClass   ResultClass
	OutcomeCode    string
	HasMediaAction bool
}

type PublicationPlannedItem struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	SourceActionID       int64
	OutcomeClass         ResultClass
	OutcomeCode          string
	NMID                 int64
}

func (item PublicationPlannedItem) IsTerminal() bool {
	return item.SourceActionID == 0
}

type ApplyPublicationPlanResultCommand struct {
	TransferID  TransferID
	PlanID      int64
	Groups      []PublicationPlannedGroup
	Items       []PublicationPlannedItem
	ActionCount int
}

func (command ApplyPublicationPlanResultCommand) Validate() error {
	if command.TransferID <= 0 || command.PlanID <= 0 || command.ActionCount < 0 {
		return errors.New("apply publication plan result command is invalid")
	}
	seenGroups := make(map[int64]struct{}, len(command.Groups))
	for index, group := range command.Groups {
		if group.GroupTargetID <= 0 || group.OutcomeCode == "" ||
			strings.TrimSpace(group.OutcomeCode) != group.OutcomeCode ||
			len(group.OutcomeCode) > 128 {
			return fmt.Errorf("publication planned group at index %d is invalid", index)
		}
		switch group.Status {
		case PublicationProjectionRunning:
			if group.OutcomeClass != "" {
				return errors.New("running publication group has terminal outcome")
			}
		case PublicationProjectionSkipped:
			if group.OutcomeClass != ResultSkipped {
				return errors.New("skipped publication group has invalid outcome")
			}
		case PublicationProjectionRejected:
			if group.OutcomeClass != ResultRejected {
				return errors.New("rejected publication group has invalid outcome")
			}
		default:
			return errors.New("publication planned group status is invalid")
		}
		if _, exists := seenGroups[group.GroupTargetID]; exists {
			return errors.New("publication planned group is duplicated")
		}
		seenGroups[group.GroupTargetID] = struct{}{}
	}
	seenItems := make(map[int64]struct{}, len(command.Items))
	for index, item := range command.Items {
		if item.GroupTargetID <= 0 || item.TransferItemTargetID <= 0 ||
			item.SourceActionID < 0 || item.NMID < 0 ||
			strings.TrimSpace(item.OutcomeCode) != item.OutcomeCode ||
			len(item.OutcomeCode) > 128 {
			return fmt.Errorf("publication planned item at index %d is invalid", index)
		}
		if item.SourceActionID > 0 {
			if item.OutcomeClass != "" || item.OutcomeCode != "" || item.NMID != 0 {
				return errors.New("action-backed publication item has terminal result")
			}
		} else if !item.OutcomeClass.IsValid() || item.OutcomeCode == "" {
			return errors.New("read-only publication item has incomplete result")
		}
		if _, exists := seenItems[item.TransferItemTargetID]; exists {
			return errors.New("publication planned item is duplicated")
		}
		seenItems[item.TransferItemTargetID] = struct{}{}
	}
	return nil
}

type PublicationPlanResultApplier struct {
	repository PublicationPlanResultRepository
}

func NewPublicationPlanResultApplier(
	repository PublicationPlanResultRepository,
) *PublicationPlanResultApplier {
	if repository == nil {
		panic("transfer publication plan result repository is nil")
	}
	return &PublicationPlanResultApplier{repository: repository}
}

func (applier *PublicationPlanResultApplier) ApplyWithin(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	command ApplyPublicationPlanResultCommand,
) error {
	if ctx == nil || tx == nil {
		return errors.New("apply publication plan result dependency is nil")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	return applier.repository.ApplyPublicationPlanResult(ctx, tx, command)
}
