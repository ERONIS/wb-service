package planning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
	transfer_preparation "github.com/ERONIS/wb-service/internal/feature/transfer/service/preparation"
	transfer_publication "github.com/ERONIS/wb-service/internal/feature/transfer/service/publication"
)

type loadedPlanningGroup struct {
	Source   transfer_publication.PublicationPlanningGroup
	Proposal transfer_preparation.StoredProposal
}

type Planner struct{}

type groupDecision struct {
	code           cardpublication_model.DecisionCode
	productKind    cardpublication_model.ActionKind
	productPayload []byte
	actionMembers  []int
	itemCodes      []cardpublication_model.DecisionCode
	itemNMIDs      []int64
	itemIMTIDs     []int64
	existingCards  map[int]contentapi.Card
	conflict       bool
}

func NewPlanner() *Planner {
	return &Planner{}
}

func (planner *Planner) Build(
	transfer transfer_model.Transfer,
	groups []loadedPlanningGroup,
	observations map[int64]cardpublication_catalog.CatalogObservation,
) (cardpublication_model.PlanDraft, error) {
	if err := transfer.Validate(); err != nil ||
		!planningMayContinue(transfer.Phase) ||
		transfer.Outcome != transfer_service.OutcomeRunning {
		return cardpublication_model.PlanDraft{}, errors.New("transfer is not ready for publication planning")
	}
	draft := cardpublication_model.PlanDraft{
		TransferID: transfer.ID,
	}
	for _, observation := range observations {
		stored, err := cardpublication_catalog.DraftObservation(observation)
		if err != nil {
			return cardpublication_model.PlanDraft{}, err
		}
		draft.Observations = append(draft.Observations, stored)
	}
	sort.Slice(draft.Observations, func(left, right int) bool {
		return draft.Observations[left].TargetID < draft.Observations[right].TargetID
	})
	desiredIdentityCounts := make(map[string]int)
	for _, group := range groups {
		for _, member := range group.Source.Members {
			key := string(group.Source.SellerID) + "\x00" + member.VendorCode
			desiredIdentityCounts[key]++
		}
	}

	for _, group := range groups {
		observation, exists := observations[group.Source.TargetID]
		if !exists {
			return cardpublication_model.PlanDraft{}, errors.New("publication group has no target observation")
		}
		if err := validateLoadedGroup(group); err != nil {
			return cardpublication_model.PlanDraft{}, err
		}
		duplicateDesired := false
		for _, member := range group.Source.Members {
			key := string(group.Source.SellerID) + "\x00" + member.VendorCode
			if desiredIdentityCounts[key] > 1 {
				duplicateDesired = true
				break
			}
		}
		decision := conflictDecision(
			len(group.Source.Members),
			cardpublication_model.DecisionRemoteIdentityConflict,
		)
		var err error
		if !duplicateDesired {
			decision, err = decideGroup(group, observation)
		}
		if err != nil {
			return cardpublication_model.PlanDraft{}, err
		}
		if err := appendDecision(
			&draft,
			group,
			decision,
		); err != nil {
			return cardpublication_model.PlanDraft{}, err
		}
	}
	if err := batchCreateGroupActions(&draft); err != nil {
		return cardpublication_model.PlanDraft{}, err
	}

	sort.Slice(draft.Actions, func(left, right int) bool {
		leftAction, rightAction := draft.Actions[left], draft.Actions[right]
		if leftAction.TargetID != rightAction.TargetID {
			return leftAction.TargetID < rightAction.TargetID
		}
		if actionRank(leftAction.Kind) != actionRank(rightAction.Kind) {
			return actionRank(leftAction.Kind) < actionRank(rightAction.Kind)
		}
		return leftAction.Position < rightAction.Position
	})
	positions := make(map[int]int, len(draft.Actions))
	for index := range draft.Actions {
		positions[draft.Actions[index].Position] = index + 1
		draft.Actions[index].Position = index + 1
	}
	for index := range draft.Items {
		if draft.Items[index].ActionPosition > 0 {
			draft.Items[index].ActionPosition = positions[draft.Items[index].ActionPosition]
		}
	}
	if err := draft.Validate(); err != nil {
		return cardpublication_model.PlanDraft{}, err
	}
	return draft, nil
}

func planningMayContinue(phase transfer_model.Phase) bool {
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

func decideGroup(
	group loadedPlanningGroup,
	observation cardpublication_catalog.CatalogObservation,
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
		itemCodes:  make([]cardpublication_model.DecisionCode, len(variants)),
		itemNMIDs:  make([]int64, len(variants)),
		itemIMTIDs: make([]int64, len(variants)),
	}
	for _, variant := range variants {
		if len(trashByVendor[variant.VendorCode]) > 0 {
			return conflictDecision(len(variants), cardpublication_model.DecisionCardInTrashConflict), nil
		}
		if len(normalByVendor[variant.VendorCode]) > 1 {
			return conflictDecision(len(variants), cardpublication_model.DecisionRemoteIdentityConflict), nil
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
		decision.code = cardpublication_model.DecisionCreateGroup
		decision.productKind = cardpublication_model.ActionCreateGroup
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
			return conflictDecision(len(variants), cardpublication_model.DecisionSubjectConflict), nil
		}
		if card.IMTID <= 0 || (remoteIMTID != 0 && remoteIMTID != card.IMTID) {
			return conflictDecision(len(variants), cardpublication_model.DecisionGroupConflict), nil
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
			return conflictDecision(len(variants), cardpublication_model.DecisionGroupCapacityConflict), nil
		}
	}

	for index, card := range existing {
		if compatibleVariant(variants[index], card) {
			decision.itemCodes[index] = cardpublication_model.DecisionAlreadyPresentCompatible
		} else {
			decision.itemCodes[index] = cardpublication_model.DecisionAlreadyPresentDifferent
		}
	}
	decision.existingCards = existing
	if len(missing) == 0 {
		decision.code = cardpublication_model.DecisionAlreadyPresentCompatible
		for _, code := range decision.itemCodes {
			if code == cardpublication_model.DecisionAlreadyPresentDifferent {
				decision.code = cardpublication_model.DecisionAlreadyPresentDifferent
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
	decision.code = cardpublication_model.DecisionAddToGroup
	decision.productKind = cardpublication_model.ActionAddToGroup
	decision.productPayload = payload
	decision.actionMembers = missing
	return decision, nil
}

func appendDecision(
	draft *cardpublication_model.PlanDraft,
	group loadedPlanningGroup,
	decision groupDecision,
) error {
	if decision.conflict {
		draft.Groups = append(draft.Groups, cardpublication_model.PlannedGroupDraft{
			GroupTargetID: group.Source.GroupTargetID,
			Status:        transfer_service.PublicationProjectionRejected,
			OutcomeClass:  transfer_service.ResultRejected,
			OutcomeCode:   decision.code,
		})
		for index, member := range group.Source.Members {
			draft.Items = append(draft.Items, cardpublication_model.PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				OutcomeClass:         transfer_service.ResultRejected,
				OutcomeCode:          decision.code,
			})
			draft.Identities = append(draft.Identities, cardpublication_model.IdentityDraft{
				SellerID:   core_domain.SellerID(group.Source.SellerID),
				VendorCode: member.VendorCode,
				State:      cardpublication_model.IdentityRemoteConflict,
				NMID:       decision.itemNMIDs[index],
				IMTID:      decision.itemIMTIDs[index],
				SubjectID:  group.Proposal.Proposal.SubjectID,
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
					mediaAction, err := buildMediaAction(group, member, links)
					if err != nil {
						return err
					}
					mediaAction.Position = len(draft.Actions) + 1
					draft.Actions = append(draft.Actions, mediaAction)
					hasMedia = true
				}
			}
		}
		draft.Groups = append(draft.Groups, cardpublication_model.PlannedGroupDraft{
			GroupTargetID:  group.Source.GroupTargetID,
			Status:         transfer_service.PublicationProjectionSkipped,
			OutcomeClass:   transfer_service.ResultSkipped,
			OutcomeCode:    decision.code,
			HasMediaAction: hasMedia,
		})
		for index, member := range group.Source.Members {
			draft.Items = append(draft.Items, cardpublication_model.PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				OutcomeClass:         transfer_service.ResultSkipped,
				OutcomeCode:          decision.itemCodes[index],
				NMID:                 decision.itemNMIDs[index],
			})
			draft.Identities = append(draft.Identities, cardpublication_model.IdentityDraft{
				SellerID:   core_domain.SellerID(group.Source.SellerID),
				VendorCode: member.VendorCode,
				State:      cardpublication_model.IdentityRemotePresent,
				NMID:       decision.itemNMIDs[index],
				IMTID:      decision.itemIMTIDs[index],
				SubjectID:  group.Proposal.Proposal.SubjectID,
			})
		}
		return nil
	}

	productAction, err := buildProductAction(group, decision)
	if err != nil {
		return err
	}
	productAction.Position = len(draft.Actions) + 1
	draft.Actions = append(draft.Actions, productAction)
	hasMedia := false
	actionMemberSet := make(map[int]struct{}, len(decision.actionMembers))
	for _, index := range decision.actionMembers {
		actionMemberSet[index] = struct{}{}
	}
	for index, member := range group.Source.Members {
		if _, mutating := actionMemberSet[index]; mutating {
			draft.Items = append(draft.Items, cardpublication_model.PlannedItemDraft{
				GroupTargetID:        group.Source.GroupTargetID,
				TransferItemTargetID: member.TransferItemTargetID,
				ActionPosition:       productAction.Position,
			})
			draft.Identities = append(draft.Identities, cardpublication_model.IdentityDraft{
				SellerID:   core_domain.SellerID(group.Source.SellerID),
				VendorCode: member.VendorCode,
				State:      cardpublication_model.IdentityRemoteMissing,
				SubjectID:  group.Proposal.Proposal.SubjectID,
			})
			links := mediaLinks(member)
			if len(links) > 0 {
				mediaAction, err := buildMediaAction(group, member, links)
				if err != nil {
					return err
				}
				mediaAction.Position = len(draft.Actions) + 1
				draft.Actions = append(draft.Actions, mediaAction)
				hasMedia = true
			}
			continue
		}
		draft.Items = append(draft.Items, cardpublication_model.PlannedItemDraft{
			GroupTargetID:        group.Source.GroupTargetID,
			TransferItemTargetID: member.TransferItemTargetID,
			OutcomeClass:         transfer_service.ResultSkipped,
			OutcomeCode:          decision.itemCodes[index],
			NMID:                 decision.itemNMIDs[index],
		})
		draft.Identities = append(draft.Identities, cardpublication_model.IdentityDraft{
			SellerID:   core_domain.SellerID(group.Source.SellerID),
			VendorCode: member.VendorCode,
			State:      cardpublication_model.IdentityRemotePresent,
			NMID:       decision.itemNMIDs[index],
			IMTID:      decision.itemIMTIDs[index],
			SubjectID:  group.Proposal.Proposal.SubjectID,
		})
		card, exists := decision.existingCards[index]
		if exists && len(card.Photos) == 0 {
			links := mediaLinks(member)
			if len(links) > 0 {
				mediaAction, err := buildMediaAction(group, member, links)
				if err != nil {
					return err
				}
				mediaAction.Position = len(draft.Actions) + 1
				draft.Actions = append(draft.Actions, mediaAction)
				hasMedia = true
			}
		}
	}
	draft.Groups = append(draft.Groups, cardpublication_model.PlannedGroupDraft{
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
) (cardpublication_model.ActionDraft, error) {
	members := make([]cardpublication_model.ActionMemberDraft, 0, len(decision.actionMembers))
	for requestIndex, sourceIndex := range decision.actionMembers {
		member := group.Source.Members[sourceIndex]
		members = append(members, cardpublication_model.ActionMemberDraft{
			GroupTargetID:        group.Source.GroupTargetID,
			TransferItemTargetID: member.TransferItemTargetID,
			RequestMemberIndex:   requestIndex,
			VendorCode:           member.VendorCode,
		})
	}
	return cardpublication_model.ActionDraft{
		Kind:           decision.productKind,
		TargetID:       group.Source.TargetID,
		RequestPayload: append([]byte(nil), decision.productPayload...),
		Members:        members,
	}, nil
}

func batchCreateGroupActions(draft *cardpublication_model.PlanDraft) error {
	if draft == nil {
		return errors.New("batch create-group actions: plan draft is nil")
	}
	createActions := make([]cardpublication_model.ActionDraft, 0)
	otherActions := make([]cardpublication_model.ActionDraft, 0, len(draft.Actions))
	for _, action := range draft.Actions {
		if action.Kind == cardpublication_model.ActionCreateGroup {
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
		return createActions[left].Position < createActions[right].Position
	})

	positionRemap := make(map[int]int, len(createActions))
	batched := make([]cardpublication_model.ActionDraft, 0, len(createActions))
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
		for oldPosition, newPosition := range remap {
			positionRemap[oldPosition] = newPosition
		}
		start = end
	}

	for index := range draft.Items {
		if newPosition, exists := positionRemap[draft.Items[index].ActionPosition]; exists {
			draft.Items[index].ActionPosition = newPosition
		}
	}
	draft.Actions = append(otherActions, batched...)
	return nil
}

func batchTargetCreateActions(
	actions []cardpublication_model.ActionDraft,
) ([]cardpublication_model.ActionDraft, map[int]int, error) {
	if len(actions) == 0 {
		return nil, nil, errors.New("batch target create-group actions: actions are empty")
	}
	targetID := actions[0].TargetID
	requests := make([]contentapi.UploadCardsGroup, len(actions))
	for index, action := range actions {
		if action.Kind != cardpublication_model.ActionCreateGroup || action.TargetID != targetID {
			return nil, nil, errors.New("batch target create-group actions: action target differs")
		}
		var request contentapi.UploadCardsRequest
		if err := cardpublication_model.DecodeExactJSON(action.RequestPayload, &request); err != nil ||
			len(request) != 1 || len(request[0].Variants) != len(action.Members) {
			return nil, nil, errors.New("batch target create-group actions: action payload is invalid")
		}
		requests[index] = request[0]
	}

	result := make([]cardpublication_model.ActionDraft, 0, (len(actions)+contentapi.MaxUploadGroups-1)/contentapi.MaxUploadGroups)
	remap := make(map[int]int, len(actions))
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
			remap[chunk[0].Position] = chunk[0].Position
			start = end
			continue
		}
		members := make([]cardpublication_model.ActionMemberDraft, 0)
		for _, action := range chunk {
			for _, member := range action.Members {
				member.RequestMemberIndex = len(members)
				members = append(members, member)
			}
		}
		result = append(result, cardpublication_model.ActionDraft{
			Position:       chunk[0].Position,
			Kind:           cardpublication_model.ActionCreateGroup,
			TargetID:       targetID,
			RequestPayload: payload,
			Members:        members,
		})
		for _, action := range chunk {
			remap[action.Position] = chunk[0].Position
		}
		start = end
	}
	return result, remap, nil
}

func buildMediaAction(
	group loadedPlanningGroup,
	member transfer_publication.PublicationPlanningMember,
	links []string,
) (cardpublication_model.ActionDraft, error) {
	payload, err := json.Marshal(struct {
		Links []string `json:"links"`
	}{Links: links})
	if err != nil {
		return cardpublication_model.ActionDraft{}, fmt.Errorf("encode media link plan: %w", err)
	}
	actionMember := cardpublication_model.ActionMemberDraft{
		GroupTargetID:        group.Source.GroupTargetID,
		TransferItemTargetID: member.TransferItemTargetID,
		RequestMemberIndex:   0,
		VendorCode:           member.VendorCode,
	}
	return cardpublication_model.ActionDraft{
		Kind:           cardpublication_model.ActionUploadMedia,
		TargetID:       group.Source.TargetID,
		RequestPayload: payload,
		Members:        []cardpublication_model.ActionMemberDraft{actionMember},
	}, nil
}

func validateLoadedGroup(group loadedPlanningGroup) error {
	if group.Proposal.GroupTargetID != group.Source.GroupTargetID ||
		group.Proposal.SourceGroupID != group.Source.SourceGroupID ||
		group.Proposal.TargetID != group.Source.TargetID ||
		core_domain.SellerID(group.Proposal.SellerID) != core_domain.SellerID(group.Source.SellerID) ||
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

func conflictDecision(size int, code cardpublication_model.DecisionCode) groupDecision {
	codes := make([]cardpublication_model.DecisionCode, size)
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

func mediaLinks(member transfer_publication.PublicationPlanningMember) []string {
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

func actionRank(kind cardpublication_model.ActionKind) int {
	switch kind {
	case cardpublication_model.ActionCreateGroup:
		return 0
	case cardpublication_model.ActionAddToGroup:
		return 1
	case cardpublication_model.ActionUploadMedia:
		return 2
	default:
		return 3
	}
}
