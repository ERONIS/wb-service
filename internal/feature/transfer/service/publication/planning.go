package publication

import (
	"errors"
	"fmt"
	"strings"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardprepare_model "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/model"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

type PublicationPlanningMember struct {
	TransferItemTargetID int64
	Position             int
	VendorCode           string
	Media                cardpipeline.Media
}

type PublicationPlanningGroup struct {
	GroupTargetID int64
	TargetID      int64
	SellerID      core_domain.SellerID
	Members       []PublicationPlanningMember
	Proposal      cardprepare_model.Proposal
}

type PublicationPlanningSource struct {
	TransferID transfer_model.TransferID
	Groups     []PublicationPlanningGroup
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
	OutcomeClass   transfer_model.ResultClass
	OutcomeCode    string
	HasMediaAction bool
}

type PublicationPlannedItem struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	SourceActionID       int64
	OutcomeClass         transfer_model.ResultClass
	OutcomeCode          string
	NMID                 int64
}

type ApplyPublicationPlanResultCommand struct {
	TransferID  transfer_model.TransferID
	PlanID      int64
	Groups      []PublicationPlannedGroup
	Items       []PublicationPlannedItem
	ActionCount int
}

func (source PublicationPlanningSource) Validate() error {
	if source.TransferID <= 0 {
		return invalidTransfer("publication planning source transfer ID is invalid")
	}
	seenGroups := make(map[int64]struct{}, len(source.Groups))
	seenItemTargets := make(map[int64]struct{})
	for groupIndex, group := range source.Groups {
		if group.GroupTargetID <= 0 || group.TargetID <= 0 ||
			len(group.Members) == 0 ||
			strings.TrimSpace(string(group.SellerID)) != string(group.SellerID) ||
			group.SellerID == "" || group.Proposal.Validate() != nil ||
			len(group.Proposal.Request[0].Variants) != len(group.Members) {
			return fmt.Errorf("publication planning group at index %d is invalid", groupIndex)
		}
		if _, exists := seenGroups[group.GroupTargetID]; exists {
			return errors.New("publication planning group is duplicated")
		}
		seenGroups[group.GroupTargetID] = struct{}{}
		for memberIndex, member := range group.Members {
			if member.TransferItemTargetID <= 0 ||
				member.Position <= 0 || member.VendorCode == "" ||
				strings.TrimSpace(member.VendorCode) != member.VendorCode ||
				(memberIndex > 0 && member.Position <= group.Members[memberIndex-1].Position) ||
				member.VendorCode != group.Proposal.Request[0].Variants[memberIndex].VendorCode {
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

func (item PublicationPlannedItem) IsTerminal() bool {
	return item.SourceActionID == 0
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
			if group.OutcomeClass != transfer_model.ResultSkipped {
				return errors.New("skipped publication group has invalid outcome")
			}
		case PublicationProjectionRejected:
			if group.OutcomeClass != transfer_model.ResultRejected {
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

// invalidTransfer оформляет ошибку некорректных данных публикации.
func invalidTransfer(message string) error {
	return core_errors.InvalidArgument("invalid transfer: %s", message)
}
