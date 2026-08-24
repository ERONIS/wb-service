package cardpublication_service

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type loadedPlanningGroup struct {
	Source   transfer_service.PublicationPlanningGroup
	Proposal cardprepare_service.StoredProposal
}

type Planner struct{}

func NewPlanner() *Planner {
	return &Planner{}
}

func (planner *Planner) Build(
	transfer transfer_service.Transfer,
	groups []loadedPlanningGroup,
	observations map[int64]CatalogObservation,
) (PlanDraft, error) {
	if err := transfer.Validate(); err != nil ||
		transfer.Phase != transfer_service.PhaseAwaitingAuthorization ||
		transfer.Outcome != transfer_service.OutcomeRunning {
		return PlanDraft{}, errors.New("transfer is not ready for publication planning")
	}
	draft := PlanDraft{
		TransferID:    transfer.ID,
		TargetSetRoot: Digest(transfer.TargetSetRoot),
	}
	observationDigests := make(map[int64]Digest, len(observations))
	for targetID, observation := range observations {
		stored, err := observationDraft(observation)
		if err != nil {
			return PlanDraft{}, err
		}
		draft.Observations = append(draft.Observations, stored)
		observationDigests[targetID] = stored.Digest
	}
	sort.Slice(draft.Observations, func(left, right int) bool {
		return draft.Observations[left].TargetID < draft.Observations[right].TargetID
	})
	desiredIdentityCounts := make(map[string]int)
	for _, group := range groups {
		for _, member := range group.Source.Members {
			key := string(group.Source.CabinetID) + "\x00" + member.VendorCode
			desiredIdentityCounts[key]++
		}
	}

	for _, group := range groups {
		observation, exists := observations[group.Source.TargetID]
		if !exists {
			return PlanDraft{}, errors.New("publication group has no target observation")
		}
		if err := validateLoadedGroup(group); err != nil {
			return PlanDraft{}, err
		}
		duplicateDesired := false
		for _, member := range group.Source.Members {
			key := string(group.Source.CabinetID) + "\x00" + member.VendorCode
			if desiredIdentityCounts[key] > 1 {
				duplicateDesired = true
				break
			}
		}
		decision := conflictDecision(
			len(group.Source.Members),
			DecisionRemoteIdentityConflict,
		)
		var err error
		if !duplicateDesired {
			decision, err = decideGroup(group, observation)
		}
		if err != nil {
			return PlanDraft{}, err
		}
		if err := appendDecision(
			&draft,
			group,
			decision,
			observationDigests[group.Source.TargetID],
		); err != nil {
			return PlanDraft{}, err
		}
	}

	sort.Slice(draft.Actions, func(left, right int) bool {
		leftAction, rightAction := draft.Actions[left], draft.Actions[right]
		if leftAction.TargetID != rightAction.TargetID {
			return leftAction.TargetID < rightAction.TargetID
		}
		if actionRank(leftAction.Kind) != actionRank(rightAction.Kind) {
			return actionRank(leftAction.Kind) < actionRank(rightAction.Kind)
		}
		return bytes.Compare(leftAction.Key[:], rightAction.Key[:]) < 0
	})
	draft.Digest = digestPlan(draft.TargetSetRoot, draft.Actions)
	if err := draft.Validate(); err != nil {
		return PlanDraft{}, err
	}
	return draft, nil
}

type groupDecision struct {
	code           DecisionCode
	productKind    ActionKind
	productPayload []byte
	actionMembers  []int
	itemCodes      []DecisionCode
	itemNMIDs      []int64
	itemIMTIDs     []int64
	conflict       bool
}

func decideGroup(
	group loadedPlanningGroup,
	observation CatalogObservation,
) (groupDecision, error) {
	variants := group.Proposal.Proposal.Request[0].Variants
	normalByVendor := make(map[string][]contentapi.Card)
	for _, card := range observation.Normal {
		normalByVendor[card.VendorCode] = append(normalByVendor[card.VendorCode], card)
	}
	trashByVendor := make(map[string][]contentapi.TrashCard)
	for _, card := range observation.Trash {
		trashByVendor[card.VendorCode] = append(trashByVendor[card.VendorCode], card)
	}

	decision := groupDecision{
		itemCodes:  make([]DecisionCode, len(variants)),
		itemNMIDs:  make([]int64, len(variants)),
		itemIMTIDs: make([]int64, len(variants)),
	}
	for _, variant := range variants {
		if len(trashByVendor[variant.VendorCode]) > 0 {
			return conflictDecision(len(variants), DecisionCardInTrashConflict), nil
		}
		if len(normalByVendor[variant.VendorCode]) > 1 {
			return conflictDecision(len(variants), DecisionRemoteIdentityConflict), nil
		}
	}

	existing := make(map[int]contentapi.Card)
	missing := make([]int, 0, len(variants))
	for index, variant := range variants {
		cards := normalByVendor[variant.VendorCode]
		if len(cards) == 0 {
			missing = append(missing, index)
			continue
		}
		existing[index] = cards[0]
		decision.itemNMIDs[index] = cards[0].NMID
		decision.itemIMTIDs[index] = cards[0].IMTID
	}
	if len(existing) == 0 {
		decision.code = DecisionCreateGroup
		decision.productKind = ActionCreateGroup
		decision.actionMembers = allIndexes(len(variants))
		decision.productPayload = append(
			[]byte(nil),
			group.Proposal.Proposal.EncodedRequest...,
		)
		return decision, nil
	}

	var remoteIMTID int64
	for _, card := range existing {
		if card.SubjectID != group.Proposal.Proposal.SubjectID {
			return conflictDecision(len(variants), DecisionSubjectConflict), nil
		}
		if card.IMTID <= 0 || (remoteIMTID != 0 && remoteIMTID != card.IMTID) {
			return conflictDecision(len(variants), DecisionGroupConflict), nil
		}
		remoteIMTID = card.IMTID
	}
	if len(missing) > 0 {
		remoteGroupMembers := make(map[int64]struct{})
		for _, card := range observation.Normal {
			if card.IMTID == remoteIMTID {
				remoteGroupMembers[card.NMID] = struct{}{}
			}
		}
		if len(remoteGroupMembers)+len(missing) > contentapi.MaxVariantsPerGroup {
			return conflictDecision(len(variants), DecisionGroupCapacityConflict), nil
		}
	}

	for index, card := range existing {
		if compatibleVariant(variants[index], card) {
			decision.itemCodes[index] = DecisionAlreadyPresentCompatible
		} else {
			decision.itemCodes[index] = DecisionAlreadyPresentDifferent
		}
	}
	if len(missing) == 0 {
		decision.code = DecisionAlreadyPresentCompatible
		for _, code := range decision.itemCodes {
			if code == DecisionAlreadyPresentDifferent {
				decision.code = DecisionAlreadyPresentDifferent
				break
			}
		}
		return decision, nil
	}

	missingVariants := make([]contentapi.UploadCard, 0, len(missing))
	for _, index := range missing {
		missingVariants = append(missingVariants, variants[index])
	}
	payload, err := json.Marshal(contentapi.UploadCardsAddRequest{
		IMTID:      remoteIMTID,
		CardsToAdd: missingVariants,
	})
	if err != nil {
		return groupDecision{}, fmt.Errorf("encode add-to-group request: %w", err)
	}
	if int64(len(payload)) > contentapi.UploadCardsAddOperation().MaxRequestBytes() {
		return groupDecision{}, errors.New("add-to-group request exceeds WB operation bound")
	}
	decision.code = DecisionAddToGroup
	decision.productKind = ActionAddToGroup
	decision.productPayload = payload
	decision.actionMembers = missing
	return decision, nil
}

func appendDecision(
	draft *PlanDraft,
	group loadedPlanningGroup,
	decision groupDecision,
	observationDigest Digest,
) error {
	if decision.conflict {
		draft.Groups = append(draft.Groups, PlannedGroupDraft{
			GroupTargetID: group.Source.GroupTargetID,
			Status:        transfer_service.PublicationProjectionRejected,
			OutcomeClass:  transfer_service.ResultRejected,
			OutcomeCode:   decision.code,
		})
		for index, member := range group.Source.Members {
			draft.Items = append(draft.Items, PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				OutcomeClass:         transfer_service.ResultRejected,
				OutcomeCode:          decision.code,
			})
			draft.Identities = append(draft.Identities, IdentityDraft{
				CabinetID:         CabinetID(group.Source.CabinetID),
				VendorCode:        member.VendorCode,
				State:             IdentityRemoteConflict,
				NMID:              decision.itemNMIDs[index],
				IMTID:             decision.itemIMTIDs[index],
				SubjectID:         group.Proposal.Proposal.SubjectID,
				ObservationDigest: observationDigest,
			})
		}
		return nil
	}

	if decision.productKind == "" {
		draft.Groups = append(draft.Groups, PlannedGroupDraft{
			GroupTargetID: group.Source.GroupTargetID,
			Status:        transfer_service.PublicationProjectionSkipped,
			OutcomeClass:  transfer_service.ResultSkipped,
			OutcomeCode:   decision.code,
		})
		for index, member := range group.Source.Members {
			draft.Items = append(draft.Items, PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				OutcomeClass:         transfer_service.ResultSkipped,
				OutcomeCode:          decision.itemCodes[index],
				NMID:                 decision.itemNMIDs[index],
			})
			draft.Identities = append(draft.Identities, IdentityDraft{
				CabinetID:         CabinetID(group.Source.CabinetID),
				VendorCode:        member.VendorCode,
				State:             IdentityRemotePresent,
				NMID:              decision.itemNMIDs[index],
				IMTID:             decision.itemIMTIDs[index],
				SubjectID:         group.Proposal.Proposal.SubjectID,
				ObservationDigest: observationDigest,
			})
		}
		return nil
	}

	productAction, err := buildProductAction(group, decision)
	if err != nil {
		return err
	}
	draft.Actions = append(draft.Actions, productAction)
	hasMedia := false
	actionMemberSet := make(map[int]struct{}, len(decision.actionMembers))
	for _, index := range decision.actionMembers {
		actionMemberSet[index] = struct{}{}
	}
	for index, member := range group.Source.Members {
		if _, mutating := actionMemberSet[index]; mutating {
			draft.Items = append(draft.Items, PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				ActionKey:            productAction.Key,
			})
			draft.Identities = append(draft.Identities, IdentityDraft{
				CabinetID:         CabinetID(group.Source.CabinetID),
				VendorCode:        member.VendorCode,
				State:             IdentityRemoteMissing,
				SubjectID:         group.Proposal.Proposal.SubjectID,
				ObservationDigest: observationDigest,
			})
			links := mediaLinks(member)
			if len(links) > 0 {
				mediaAction, err := buildMediaAction(group, member, links, productAction.Key)
				if err != nil {
					return err
				}
				draft.Actions = append(draft.Actions, mediaAction)
				hasMedia = true
			}
			continue
		}
		draft.Items = append(draft.Items, PlannedItemDraft{
			GroupTargetID:        group.Source.GroupTargetID,
			TransferItemTargetID: member.TransferItemTargetID,
			OutcomeClass:         transfer_service.ResultSkipped,
			OutcomeCode:          decision.itemCodes[index],
			NMID:                 decision.itemNMIDs[index],
		})
		draft.Identities = append(draft.Identities, IdentityDraft{
			CabinetID:         CabinetID(group.Source.CabinetID),
			VendorCode:        member.VendorCode,
			State:             IdentityRemotePresent,
			NMID:              decision.itemNMIDs[index],
			IMTID:             decision.itemIMTIDs[index],
			SubjectID:         group.Proposal.Proposal.SubjectID,
			ObservationDigest: observationDigest,
		})
	}
	draft.Groups = append(draft.Groups, PlannedGroupDraft{
		GroupTargetID:  group.Source.GroupTargetID,
		Status:         transfer_service.PublicationProjectionRunning,
		OutcomeCode:    decision.code,
		HasMediaAction: hasMedia,
	})
	return nil
}

func buildProductAction(
	group loadedPlanningGroup,
	decision groupDecision,
) (ActionDraft, error) {
	members := make([]ActionMemberDraft, 0, len(decision.actionMembers))
	for requestIndex, sourceIndex := range decision.actionMembers {
		member := group.Source.Members[sourceIndex]
		members = append(members, ActionMemberDraft{
			GroupTargetID:        group.Source.GroupTargetID,
			TransferItemTargetID: member.TransferItemTargetID,
			RequestMemberIndex:   requestIndex,
			VendorCode:           member.VendorCode,
		})
	}
	requestDigest := digestParts("cardpublication-request:v1", decision.productPayload)
	memberDigest := digestMembers(members)
	key := digestParts(
		"cardpublication-action:v1",
		[]byte(decision.productKind),
		[]byte(strconv.FormatInt(group.Source.TargetID, 10)),
		[]byte(strconv.FormatInt(group.Source.GroupTargetID, 10)),
		requestDigest[:],
		memberDigest[:],
	)
	return ActionDraft{
		Key:             key,
		Kind:            decision.productKind,
		TargetID:        group.Source.TargetID,
		RequestDigest:   requestDigest,
		RequestPayload:  append([]byte(nil), decision.productPayload...),
		MemberSetDigest: memberDigest,
		Members:         members,
	}, nil
}

func buildMediaAction(
	group loadedPlanningGroup,
	member transfer_service.PublicationPlanningMember,
	links []string,
	productActionKey Digest,
) (ActionDraft, error) {
	payload, err := json.Marshal(struct {
		Links []string `json:"links"`
	}{Links: links})
	if err != nil {
		return ActionDraft{}, fmt.Errorf("encode media link plan: %w", err)
	}
	actionMember := ActionMemberDraft{
		GroupTargetID:        group.Source.GroupTargetID,
		TransferItemTargetID: member.TransferItemTargetID,
		RequestMemberIndex:   0,
		VendorCode:           member.VendorCode,
	}
	requestDigest := digestParts("cardpublication-media-template:v1", payload)
	memberDigest := digestMembers([]ActionMemberDraft{actionMember})
	linkRoot := digestParts("cardpublication-media-links:v1", payload)
	key := digestParts(
		"cardpublication-action:v1",
		[]byte(ActionUploadMedia),
		[]byte(strconv.FormatInt(group.Source.TargetID, 10)),
		[]byte(strconv.FormatInt(group.Source.GroupTargetID, 10)),
		productActionKey[:],
		requestDigest[:],
		memberDigest[:],
		linkRoot[:],
	)
	return ActionDraft{
		Key:              key,
		Kind:             ActionUploadMedia,
		TargetID:         group.Source.TargetID,
		RequestDigest:    requestDigest,
		RequestPayload:   payload,
		MemberSetDigest:  memberDigest,
		MediaLinkSetRoot: linkRoot,
		Members:          []ActionMemberDraft{actionMember},
	}, nil
}

func validateLoadedGroup(group loadedPlanningGroup) error {
	if group.Proposal.GroupTargetID != group.Source.GroupTargetID ||
		group.Proposal.SourceGroupID != group.Source.SourceGroupID ||
		group.Proposal.TargetID != group.Source.TargetID ||
		CabinetID(group.Proposal.CabinetID) != CabinetID(group.Source.CabinetID) ||
		int64(group.Proposal.PreparationGroupID) != group.Source.PreparationGroupID ||
		Digest(group.Proposal.Proposal.ProposalRoot) != Digest(group.Source.ProposalRoot) ||
		len(group.Proposal.Members) != len(group.Source.Members) {
		return errors.New("prepared proposal differs from publication planning source")
	}
	for index, member := range group.Source.Members {
		prepared := group.Proposal.Members[index]
		if member.TransferItemID != prepared.TransferItemID ||
			member.Position != prepared.ItemPosition || member.VendorCode != prepared.VendorCode {
			return errors.New("prepared proposal member differs from publication source")
		}
	}
	return nil
}

func compatibleVariant(desired contentapi.UploadCard, existing contentapi.Card) bool {
	if desired.VendorCode != existing.VendorCode || desired.KIZMarked != existing.KIZMarked ||
		desired.Title != existing.Title || desired.Description != existing.Description ||
		desired.Brand != existing.Brand ||
		desired.Dimensions.Length != existing.Dimensions.Length ||
		desired.Dimensions.Width != existing.Dimensions.Width ||
		desired.Dimensions.Height != existing.Dimensions.Height ||
		desired.Dimensions.WeightBrutto != existing.Dimensions.WeightBrutto {
		return false
	}
	existingCharacteristics := make(map[int64]any, len(existing.Characteristics))
	for _, characteristic := range existing.Characteristics {
		existingCharacteristics[characteristic.ID] = characteristic.Value
	}
	for _, characteristic := range desired.Characteristics {
		if !jsonValuesEqual(characteristic.Value, existingCharacteristics[characteristic.ID]) {
			return false
		}
	}
	if len(desired.Sizes) != len(existing.Sizes) {
		return false
	}
	for index, size := range desired.Sizes {
		remote := existing.Sizes[index]
		if size.TechSize != "" && size.TechSize != remote.TechSize {
			return false
		}
		if size.WBSize != "" && size.WBSize != remote.WBSize {
			return false
		}
		if len(size.SKUs) > 0 && !reflect.DeepEqual(size.SKUs, remote.SKUs) {
			return false
		}
	}
	return true
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func conflictDecision(size int, code DecisionCode) groupDecision {
	codes := make([]DecisionCode, size)
	for index := range codes {
		codes[index] = code
	}
	return groupDecision{
		code:       code,
		itemCodes:  codes,
		itemNMIDs:  make([]int64, size),
		itemIMTIDs: make([]int64, size),
		conflict:   true,
	}
}

func allIndexes(size int) []int {
	indexes := make([]int, size)
	for index := range indexes {
		indexes[index] = index
	}
	return indexes
}

func mediaLinks(member transfer_service.PublicationPlanningMember) []string {
	links := make([]string, 0, len(member.Media.Photos)+1)
	for _, link := range member.Media.Photos {
		if link != "" {
			links = append(links, link)
		}
	}
	if member.Media.Video != "" {
		links = append(links, member.Media.Video)
	}
	return links
}

func digestMembers(members []ActionMemberDraft) Digest {
	parts := make([][]byte, 0, len(members)*4)
	for _, member := range members {
		parts = append(
			parts,
			[]byte(strconv.FormatInt(member.GroupTargetID, 10)),
			[]byte(strconv.FormatInt(member.TransferItemTargetID, 10)),
			[]byte(strconv.Itoa(member.RequestMemberIndex)),
			[]byte(member.VendorCode),
		)
	}
	return digestParts("cardpublication-members:v1", parts...)
}

func digestPlan(targetSetRoot Digest, actions []ActionDraft) Digest {
	parts := make([][]byte, 0, 1+len(actions)*7)
	parts = append(parts, targetSetRoot[:])
	for _, action := range actions {
		parts = append(
			parts,
			action.Key[:],
			[]byte(action.Kind),
			[]byte(strconv.FormatInt(action.TargetID, 10)),
			action.MemberSetDigest[:],
			action.RequestDigest[:],
			action.MediaLinkSetRoot[:],
		)
	}
	return digestParts("cardpublication-plan:v1", parts...)
}

func digestParts(domain string, parts ...[]byte) Digest {
	hash := sha256.New()
	writeDigestPart(hash, []byte(domain))
	for _, part := range parts {
		writeDigestPart(hash, part)
	}
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestPart(writer digestWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func actionRank(kind ActionKind) int {
	switch kind {
	case ActionCreateGroup:
		return 0
	case ActionAddToGroup:
		return 1
	case ActionUploadMedia:
		return 2
	default:
		return 3
	}
}
