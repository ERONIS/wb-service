package planning

import (
	"bytes"
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
		!planningMayContinue(transfer.Phase) ||
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
	if err := batchCreateGroupActions(&draft); err != nil {
		return PlanDraft{}, err
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
	draft.Digest = digestPlan(draft.TargetSetRoot, draft.Groups, draft.Items, draft.Actions)
	if err := draft.Validate(); err != nil {
		return PlanDraft{}, err
	}
	return draft, nil
}

func planningMayContinue(phase transfer_service.Phase) bool {
	switch phase {
	case transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia:
		return true
	default:
		return false
	}
}

type groupDecision struct {
	code           DecisionCode
	productKind    ActionKind
	productPayload []byte
	actionMembers  []int
	itemCodes      []DecisionCode
	itemNMIDs      []int64
	itemIMTIDs     []int64
	existingCards  map[int]contentapi.Card
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
	decision.existingCards = existing
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
		hasMedia := false
		for index, member := range group.Source.Members {
			card, exists := decision.existingCards[index]
			if exists && len(card.Photos) == 0 {
				links := mediaLinks(member)
				if len(links) > 0 {
					mediaAction, err := buildMediaAction(group, member, links, Digest{})
					if err != nil {
						return err
					}
					draft.Actions = append(draft.Actions, mediaAction)
					hasMedia = true
				}
			}
		}
		draft.Groups = append(draft.Groups, PlannedGroupDraft{
			GroupTargetID:  group.Source.GroupTargetID,
			Status:         transfer_service.PublicationProjectionSkipped,
			OutcomeClass:   transfer_service.ResultSkipped,
			OutcomeCode:    decision.code,
			HasMediaAction: hasMedia,
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
		card, exists := decision.existingCards[index]
		if exists && len(card.Photos) == 0 {
			links := mediaLinks(member)
			if len(links) > 0 {
				mediaAction, err := buildMediaAction(group, member, links, Digest{})
				if err != nil {
					return err
				}
				draft.Actions = append(draft.Actions, mediaAction)
				hasMedia = true
			}
		}
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

func batchCreateGroupActions(draft *PlanDraft) error {
	if draft == nil {
		return errors.New("batch create-group actions: plan draft is nil")
	}
	createActions := make([]ActionDraft, 0)
	otherActions := make([]ActionDraft, 0, len(draft.Actions))
	for _, action := range draft.Actions {
		if action.Kind == ActionCreateGroup {
			createActions = append(createActions, action)
			continue
		}
		otherActions = append(otherActions, action)
	}
	if len(createActions) < 2 {
		return nil
	}
	sort.Slice(createActions, func(left, right int) bool {
		if createActions[left].TargetID != createActions[right].TargetID {
			return createActions[left].TargetID < createActions[right].TargetID
		}
		return bytes.Compare(createActions[left].Key[:], createActions[right].Key[:]) < 0
	})

	oldProductKeyByItem := make(map[int64]Digest, len(draft.Items))
	for _, item := range draft.Items {
		if item.ActionKey != (Digest{}) {
			oldProductKeyByItem[item.TransferItemTargetID] = item.ActionKey
		}
	}
	keyRemap := make(map[Digest]Digest, len(createActions))
	batched := make([]ActionDraft, 0, len(createActions))
	for start := 0; start < len(createActions); {
		targetID := createActions[start].TargetID
		end := start
		for end < len(createActions) && createActions[end].TargetID == targetID {
			end++
		}
		combined, remap, err := batchTargetCreateActions(createActions[start:end])
		if err != nil {
			return err
		}
		batched = append(batched, combined...)
		for oldKey, newKey := range remap {
			keyRemap[oldKey] = newKey
		}
		start = end
	}

	for index := range draft.Items {
		if newKey, exists := keyRemap[draft.Items[index].ActionKey]; exists {
			draft.Items[index].ActionKey = newKey
		}
	}
	for index := range otherActions {
		action := &otherActions[index]
		if action.Kind != ActionUploadMedia || len(action.Members) != 1 {
			continue
		}
		oldProductKey, exists := oldProductKeyByItem[action.Members[0].TransferItemTargetID]
		if !exists {
			continue
		}
		newProductKey, remapped := keyRemap[oldProductKey]
		if !remapped || newProductKey == oldProductKey {
			continue
		}
		action.Key = mediaActionKey(*action, newProductKey)
	}
	draft.Actions = append(otherActions, batched...)
	return nil
}

func batchTargetCreateActions(
	actions []ActionDraft,
) ([]ActionDraft, map[Digest]Digest, error) {
	if len(actions) == 0 {
		return nil, nil, errors.New("batch target create-group actions: actions are empty")
	}
	targetID := actions[0].TargetID
	requests := make([]contentapi.UploadCardsGroup, len(actions))
	for index, action := range actions {
		if action.Kind != ActionCreateGroup || action.TargetID != targetID {
			return nil, nil, errors.New("batch target create-group actions: action target differs")
		}
		var request contentapi.UploadCardsRequest
		if err := decodeExactJSON(action.RequestPayload, &request); err != nil ||
			len(request) != 1 || len(request[0].Variants) != len(action.Members) {
			return nil, nil, errors.New("batch target create-group actions: action payload is invalid")
		}
		requests[index] = request[0]
	}

	result := make([]ActionDraft, 0, (len(actions)+contentapi.MaxUploadGroups-1)/contentapi.MaxUploadGroups)
	remap := make(map[Digest]Digest, len(actions))
	for start := 0; start < len(actions); {
		end := start
		var payload []byte
		for end < len(actions) && end-start < contentapi.MaxUploadGroups {
			candidate, err := json.Marshal(contentapi.UploadCardsRequest(requests[start : end+1]))
			if err != nil {
				return nil, nil, fmt.Errorf("encode batched create-group request: %w", err)
			}
			if int64(len(candidate)) > contentapi.UploadCardsOperation().MaxRequestBytes() {
				break
			}
			payload = candidate
			end++
		}
		if end == start || len(payload) == 0 {
			return nil, nil, errors.New("create-group request exceeds WB operation bound")
		}

		chunk := actions[start:end]
		if len(chunk) == 1 {
			result = append(result, chunk[0])
			remap[chunk[0].Key] = chunk[0].Key
			start = end
			continue
		}
		members := make([]ActionMemberDraft, 0)
		for _, action := range chunk {
			for _, member := range action.Members {
				member.RequestMemberIndex = len(members)
				members = append(members, member)
			}
		}
		requestDigest := digestParts("cardpublication-request:v1", payload)
		memberDigest := digestMembers(members)
		key := digestParts(
			"cardpublication-create-batch:v1",
			[]byte(strconv.FormatInt(targetID, 10)),
			requestDigest[:],
			memberDigest[:],
		)
		result = append(result, ActionDraft{
			Key:             key,
			Kind:            ActionCreateGroup,
			TargetID:        targetID,
			RequestDigest:   requestDigest,
			RequestPayload:  payload,
			MemberSetDigest: memberDigest,
			Members:         members,
		})
		for _, action := range chunk {
			remap[action.Key] = key
		}
		start = end
	}
	return result, remap, nil
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
	action := ActionDraft{
		Kind:             ActionUploadMedia,
		TargetID:         group.Source.TargetID,
		RequestDigest:    requestDigest,
		RequestPayload:   payload,
		MemberSetDigest:  memberDigest,
		MediaLinkSetRoot: linkRoot,
		Members:          []ActionMemberDraft{actionMember},
	}
	action.Key = mediaActionKey(action, productActionKey)
	return action, nil
}

func mediaActionKey(action ActionDraft, productActionKey Digest) Digest {
	groupTargetID := int64(0)
	if len(action.Members) == 1 {
		groupTargetID = action.Members[0].GroupTargetID
	}
	return digestParts(
		"cardpublication-action:v1",
		[]byte(ActionUploadMedia),
		[]byte(strconv.FormatInt(action.TargetID, 10)),
		[]byte(strconv.FormatInt(groupTargetID, 10)),
		productActionKey[:],
		action.RequestDigest[:],
		action.MemberSetDigest[:],
		action.MediaLinkSetRoot[:],
	)
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

func digestPlan(
	targetSetRoot Digest,
	groups []PlannedGroupDraft,
	items []PlannedItemDraft,
	actions []ActionDraft,
) Digest {
	parts := make([][]byte, 0, 1+len(groups)*6+len(items)*7+len(actions)*7)
	parts = append(parts, targetSetRoot[:])
	for _, group := range groups {
		parts = append(
			parts,
			[]byte("group"),
			[]byte(strconv.FormatInt(group.GroupTargetID, 10)),
			[]byte(group.Status),
			[]byte(group.OutcomeClass),
			[]byte(group.OutcomeCode),
			[]byte(strconv.FormatBool(group.HasMediaAction)),
		)
	}
	for _, item := range items {
		parts = append(
			parts,
			[]byte("item"),
			[]byte(strconv.FormatInt(item.GroupTargetID, 10)),
			[]byte(strconv.FormatInt(item.TransferItemTargetID, 10)),
			item.ActionKey[:],
			[]byte(item.OutcomeClass),
			[]byte(item.OutcomeCode),
			[]byte(strconv.FormatInt(item.NMID, 10)),
		)
	}
	for _, action := range actions {
		parts = append(
			parts,
			[]byte("action"),
			action.Key[:],
			[]byte(action.Kind),
			[]byte(strconv.FormatInt(action.TargetID, 10)),
			action.MemberSetDigest[:],
			action.RequestDigest[:],
			action.MediaLinkSetRoot[:],
		)
	}
	return digestParts("cardpublication-plan:v2", parts...)
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
