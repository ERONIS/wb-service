package cardpublication_service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type CabinetID string
type Digest [sha256.Size]byte

func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}

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
	CabinetID   CabinetID
	Digest      Digest
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
	Key              Digest
	Kind             ActionKind
	TargetID         int64
	RequestDigest    Digest
	RequestPayload   []byte
	MemberSetDigest  Digest
	MediaLinkSetRoot Digest
	Members          []ActionMemberDraft
}

type PlannedItemDraft struct {
	GroupTargetID        int64
	TransferItemTargetID int64
	ActionKey            Digest
	OutcomeClass         transfer_service.ResultClass
	OutcomeCode          DecisionCode
	NMID                 int64
}

type PlannedGroupDraft struct {
	GroupTargetID  int64
	Status         transfer_service.PublicationProjectionStatus
	OutcomeClass   transfer_service.ResultClass
	OutcomeCode    DecisionCode
	HasMediaAction bool
}

type IdentityDraft struct {
	CabinetID         CabinetID
	VendorCode        string
	State             IdentityState
	NMID              int64
	IMTID             int64
	SubjectID         int64
	ObservationDigest Digest
}

type PlanDraft struct {
	TransferID    transfer_service.TransferID
	TargetSetRoot Digest
	Digest        Digest
	Observations  []ObservationDraft
	Actions       []ActionDraft
	Groups        []PlannedGroupDraft
	Items         []PlannedItemDraft
	Identities    []IdentityDraft
}

func (draft PlanDraft) Validate() error {
	if draft.TransferID <= 0 || draft.TargetSetRoot == (Digest{}) ||
		draft.Digest == (Digest{}) {
		return errors.New("publication plan draft identity is invalid")
	}
	seenActions := make(map[Digest]struct{}, len(draft.Actions))
	for index, action := range draft.Actions {
		if action.Key == (Digest{}) || action.RequestDigest == (Digest{}) ||
			action.MemberSetDigest == (Digest{}) || action.TargetID <= 0 ||
			len(action.RequestPayload) == 0 || len(action.Members) == 0 {
			return fmt.Errorf("publication action at index %d is invalid", index)
		}
		switch action.Kind {
		case ActionCreateGroup, ActionAddToGroup:
			if action.MediaLinkSetRoot != (Digest{}) {
				return errors.New("product action has media link root")
			}
		case ActionUploadMedia:
			if action.MediaLinkSetRoot == (Digest{}) || len(action.Members) != 1 {
				return errors.New("media action shape is invalid")
			}
		default:
			return errors.New("publication action kind is invalid")
		}
		if _, exists := seenActions[action.Key]; exists {
			return errors.New("publication action key is duplicated")
		}
		seenActions[action.Key] = struct{}{}
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
	return nil
}

type PersistedAction struct {
	ID  int64
	Key Digest
}

type PersistedPlan struct {
	ID      int64
	Actions []PersistedAction
}
