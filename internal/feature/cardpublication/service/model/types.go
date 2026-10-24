package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type ActionKind string

const (
	ActionCreateGroup ActionKind = "create_group"
	ActionAddToGroup  ActionKind = "add_to_group"
	ActionUploadMedia ActionKind = "upload_media"
)

type DecisionCode string

const (
	DecisionCreateGroup              DecisionCode = "CREATE_GROUP"
	DecisionAddToGroup               DecisionCode = "ADD_TO_GROUP"
	DecisionAlreadyPresentCompatible DecisionCode = "ALREADY_PRESENT_COMPATIBLE"
	DecisionAlreadyPresentDifferent  DecisionCode = "ALREADY_PRESENT_DIFFERENT_UNTOUCHED"
	DecisionCardInTrashConflict      DecisionCode = "CARD_IN_TRASH_CONFLICT"
	DecisionGroupConflict            DecisionCode = "GROUP_CONFLICT"
	DecisionSubjectConflict          DecisionCode = "SUBJECT_CONFLICT"
	DecisionGroupCapacityConflict    DecisionCode = "GROUP_CAPACITY_CONFLICT"
	DecisionRemoteIdentityConflict   DecisionCode = "REMOTE_IDENTITY_CONFLICT"
)

type IdentityState string

const (
	IdentityRemoteMissing  IdentityState = "remote_missing"
	IdentityRemotePresent  IdentityState = "remote_present"
	IdentityRemoteConflict IdentityState = "remote_conflict"
)

type ObservationDraft struct {
	TargetID    int64
	SellerID    domain.SellerID
	NormalCount int
	TrashCount  int
	Payload     []byte
	ObservedAt  time.Time
}

type ActionMemberDraft struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	RequestMemberIndex   int
	VendorCode           string
}

type ActionDraft struct {
	Position       int
	Kind           ActionKind
	TargetID       int64
	RequestPayload []byte
	Members        []ActionMemberDraft
}

type PlannedItemDraft struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	ActionPosition       int
	OutcomeClass         transfer_model.ResultClass
	OutcomeCode          DecisionCode
	NMID                 int64
}

type PlannedGroupDraft struct {
	GroupTargetID  int64
	Status         transfer_publication.PublicationProjectionStatus
	OutcomeClass   transfer_model.ResultClass
	OutcomeCode    DecisionCode
	HasMediaAction bool
}

type IdentityDraft struct {
	SellerID   domain.SellerID
	VendorCode string
	State      IdentityState
	NMID       int64
	IMTID      int64
	SubjectID  int64
}

type PlanDraft struct {
	TransferID   transfer_model.TransferID
	Observations []ObservationDraft
	Actions      []ActionDraft
	Groups       []PlannedGroupDraft
	Items        []PlannedItemDraft
	Identities   []IdentityDraft
}

func (draft PlanDraft) Validate() error {
	if draft.TransferID <= 0 {
		return errors.New("publication plan draft identity is invalid")
	}
	seenActions := make(map[int]struct{}, len(draft.Actions))
	for index, action := range draft.Actions {
		if action.Position <= 0 || action.TargetID <= 0 ||
			len(action.RequestPayload) == 0 || len(action.Members) == 0 {
			return fmt.Errorf("publication action at index %d is invalid", index)
		}
		switch action.Kind {
		case ActionCreateGroup, ActionAddToGroup:
		case ActionUploadMedia:
			if len(action.Members) != 1 {
				return errors.New("media action shape is invalid")
			}
		default:
			return errors.New("publication action kind is invalid")
		}
		if _, exists := seenActions[action.Position]; exists {
			return errors.New("publication action position is duplicated")
		}
		seenActions[action.Position] = struct{}{}
		seenMembers := make(map[int64]struct{}, len(action.Members))
		for memberIndex, member := range action.Members {
			if member.GroupTargetID <= 0 || member.TransferItemTargetID <= 0 ||
				member.RequestMemberIndex != memberIndex || member.VendorCode == "" ||
				strings.TrimSpace(member.VendorCode) != member.VendorCode {
				return fmt.Errorf(
					"publication action member at index %d/%d is invalid",
					index,
					memberIndex,
				)
			}
			if _, exists := seenMembers[member.TransferItemTargetID]; exists {
				return errors.New("publication action member is duplicated")
			}
			seenMembers[member.TransferItemTargetID] = struct{}{}
		}
	}
	for _, item := range draft.Items {
		if item.ActionPosition < 0 {
			return errors.New("publication item action position is invalid")
		}
		if item.ActionPosition > 0 {
			if _, exists := seenActions[item.ActionPosition]; !exists {
				return errors.New("publication item refers to missing action")
			}
		}
	}
	return nil
}

type PersistedAction struct {
	ID       int64
	Position int
}

type PersistedPlan struct {
	ID      int64
	Actions []PersistedAction
}
