package planning

import (
	"encoding/json"
	"fmt"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpipeline "github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	cardprepare_service "github.com/ERONIS/wb-service/internal/feature/cardprepare/service"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestPlanningMayContinueAcrossIncrementalPipeline(t *testing.T) {
	t.Parallel()

	for _, phase := range []transfer_service.Phase{
		transfer_service.PhasePreparing,
		transfer_service.PhaseAwaitingAuthorization,
		transfer_service.PhasePublishing,
		transfer_service.PhaseReconciling,
		transfer_service.PhaseMedia,
	} {
		if !planningMayContinue(phase) {
			t.Fatalf("planning stopped in active phase %q", phase)
		}
	}
	for _, phase := range []transfer_service.Phase{
		transfer_service.PhaseInitializing,
		transfer_service.PhaseFinished,
	} {
		if planningMayContinue(phase) {
			t.Fatalf("planning continued in inactive phase %q", phase)
		}
	}
}

func TestPlanDigestDistinguishesZeroActionWaves(t *testing.T) {
	t.Parallel()

	root := digestParts("test-target-root", []byte("target"))
	firstGroups := []PlannedGroupDraft{{
		GroupTargetID: 1,
		Status:        transfer_service.PublicationProjectionSkipped,
		OutcomeClass:  transfer_service.ResultSkipped,
		OutcomeCode:   DecisionAlreadyPresentCompatible,
	}}
	firstItems := []PlannedItemDraft{{
		GroupTargetID:        1,
		TransferItemTargetID: 10,
		OutcomeClass:         transfer_service.ResultSkipped,
		OutcomeCode:          DecisionAlreadyPresentCompatible,
		NMID:                 100,
	}}
	secondGroups := []PlannedGroupDraft{{
		GroupTargetID: 2,
		Status:        transfer_service.PublicationProjectionSkipped,
		OutcomeClass:  transfer_service.ResultSkipped,
		OutcomeCode:   DecisionAlreadyPresentCompatible,
	}}
	secondItems := []PlannedItemDraft{{
		GroupTargetID:        2,
		TransferItemTargetID: 20,
		OutcomeClass:         transfer_service.ResultSkipped,
		OutcomeCode:          DecisionAlreadyPresentCompatible,
		NMID:                 200,
	}}

	first := digestPlan(root, firstGroups, firstItems, nil)
	second := digestPlan(root, secondGroups, secondItems, nil)
	if first == second {
		t.Fatal("different zero-action waves received the same plan digest")
	}
	if repeated := digestPlan(root, firstGroups, firstItems, nil); repeated != first {
		t.Fatal("plan digest is not deterministic")
	}
}

func TestBatchCreateGroupActionsUsesWBMaximum(t *testing.T) {
	t.Parallel()

	const actionCount = 205
	draft := PlanDraft{
		Actions: make([]ActionDraft, 0, actionCount+1),
		Items:   make([]PlannedItemDraft, 0, actionCount),
	}
	for index := 0; index < actionCount; index++ {
		action := testCreateGroupAction(t, int64(index+1), 10)
		draft.Actions = append(draft.Actions, action)
		draft.Items = append(draft.Items, PlannedItemDraft{
			GroupTargetID:        int64(index + 1),
			TransferItemTargetID: int64(10_000 + index),
			ActionKey:            action.Key,
		})
	}
	mediaPayload := []byte(`{"links":["https://example.test/image.jpg"]}`)
	mediaMember := ActionMemberDraft{
		GroupTargetID:        1,
		TransferItemTargetID: 10_000,
		RequestMemberIndex:   0,
		VendorCode:           "SKU-000",
	}
	mediaAction := ActionDraft{
		Kind:             ActionUploadMedia,
		TargetID:         10,
		RequestDigest:    digestParts("cardpublication-media-template:v1", mediaPayload),
		RequestPayload:   mediaPayload,
		MemberSetDigest:  digestMembers([]ActionMemberDraft{mediaMember}),
		MediaLinkSetRoot: digestParts("cardpublication-media-links:v1", mediaPayload),
		Members:          []ActionMemberDraft{mediaMember},
	}
	oldProductKey := draft.Items[0].ActionKey
	mediaAction.Key = mediaActionKey(mediaAction, oldProductKey)
	oldMediaKey := mediaAction.Key
	draft.Actions = append(draft.Actions, mediaAction)

	if err := batchCreateGroupActions(&draft); err != nil {
		t.Fatalf("batch create-group actions: %v", err)
	}

	createActions := make([]ActionDraft, 0, 3)
	var storedMedia ActionDraft
	for _, action := range draft.Actions {
		switch action.Kind {
		case ActionCreateGroup:
			createActions = append(createActions, action)
		case ActionUploadMedia:
			storedMedia = action
		}
	}
	if len(createActions) != 3 {
		t.Fatalf("create action count = %d, want 3", len(createActions))
	}
	groupCounts := make([]int, 0, len(createActions))
	knownKeys := make(map[Digest]struct{}, len(createActions))
	memberCount := 0
	for _, action := range createActions {
		knownKeys[action.Key] = struct{}{}
		var request contentapi.UploadCardsRequest
		if err := decodeExactJSON(action.RequestPayload, &request); err != nil {
			t.Fatalf("decode batched request: %v", err)
		}
		if len(request) > contentapi.MaxUploadGroups {
			t.Fatalf("request group count = %d, maximum = %d", len(request), contentapi.MaxUploadGroups)
		}
		if int64(len(action.RequestPayload)) > contentapi.UploadCardsOperation().MaxRequestBytes() {
			t.Fatalf("request payload exceeds operation bound: %d", len(action.RequestPayload))
		}
		groupCounts = append(groupCounts, len(request))
		flattened := make([]string, 0, len(action.Members))
		for _, group := range request {
			for _, variant := range group.Variants {
				flattened = append(flattened, variant.VendorCode)
			}
		}
		if len(flattened) != len(action.Members) {
			t.Fatalf("request members = %d, journal members = %d", len(flattened), len(action.Members))
		}
		for index, member := range action.Members {
			if member.RequestMemberIndex != index || member.VendorCode != flattened[index] {
				t.Fatalf("member %d differs from request", index)
			}
			if !requestHasVendorInSubject(request, member.VendorCode, 777) {
				t.Fatalf("member %d cannot be resolved against its request group", index)
			}
		}
		memberCount += len(action.Members)
	}
	if fmt.Sprint(groupCounts) != "[100 100 5]" {
		t.Fatalf("request group counts = %v, want [100 100 5]", groupCounts)
	}
	if memberCount != actionCount {
		t.Fatalf("batched member count = %d, want %d", memberCount, actionCount)
	}
	for index, item := range draft.Items {
		if _, exists := knownKeys[item.ActionKey]; !exists {
			t.Fatalf("item %d points to an unpersisted product action", index)
		}
	}
	if storedMedia.Key == oldMediaKey {
		t.Fatal("media action key was not rebound to the batched product action")
	}
	if want := mediaActionKey(storedMedia, draft.Items[0].ActionKey); storedMedia.Key != want {
		t.Fatal("media action key does not reference the batched product action")
	}
}

func requestHasVendorInSubject(
	request contentapi.UploadCardsRequest,
	vendorCode string,
	subjectID int64,
) bool {
	matches := 0
	for _, group := range request {
		for _, variant := range group.Variants {
			if variant.VendorCode != vendorCode {
				continue
			}
			if group.SubjectID != subjectID {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

func testCreateGroupAction(t *testing.T, groupTargetID, targetID int64) ActionDraft {
	t.Helper()
	vendorCode := fmt.Sprintf("SKU-%03d", groupTargetID-1)
	payload, err := json.Marshal(contentapi.UploadCardsRequest{{
		SubjectID: 777,
		Variants:  []contentapi.UploadCard{{VendorCode: vendorCode}},
	}})
	if err != nil {
		t.Fatalf("encode create request: %v", err)
	}
	members := []ActionMemberDraft{{
		GroupTargetID:        groupTargetID,
		TransferItemTargetID: 10_000 + groupTargetID - 1,
		RequestMemberIndex:   0,
		VendorCode:           vendorCode,
	}}
	requestDigest := digestParts("cardpublication-request:v1", payload)
	memberDigest := digestMembers(members)
	return ActionDraft{
		Key: digestParts(
			"test-create-action:v1",
			[]byte(fmt.Sprint(groupTargetID)),
			requestDigest[:],
			memberDigest[:],
		),
		Kind:            ActionCreateGroup,
		TargetID:        targetID,
		RequestDigest:   requestDigest,
		RequestPayload:  payload,
		MemberSetDigest: memberDigest,
		Members:         members,
	}
}

func TestPlannerUploadsMediaForExistingCardWithZeroPhotos(t *testing.T) {
	t.Parallel()

	group := loadedPlanningGroup{
		Source: transfer_service.PublicationPlanningGroup{
			GroupTargetID:      1,
			SourceGroupID:      10,
			TargetID:           100,
			CabinetID:          "test-cabinet",
			PreparationGroupID: 1,
			ProposalRoot:       Digest{1},
			Members: []transfer_service.PublicationPlanningMember{
				{
					TransferItemID:       101,
					TransferItemTargetID: 201,
					Position:             1,
					VendorCode:           "SKU-EXISTING",
					Media: cardpipeline.Media{
						Photos: []string{"https://example.com/photo1.jpg"},
					},
				},
			},
		},
		Proposal: cardprepare_service.StoredProposal{
			GroupTargetID:      1,
			SourceGroupID:      10,
			TargetID:           100,
			CabinetID:          "test-cabinet",
			PreparationGroupID: 1,
			Members: []cardprepare_service.PreparedMember{
				{
					TransferItemID: 101,
					ItemPosition:   1,
					VendorCode:     "SKU-EXISTING",
				},
			},
			Proposal: cardprepare_service.Proposal{
				SubjectID:    777,
				ProposalRoot: Digest{1},
				Request: contentapi.UploadCardsRequest{
					{
						SubjectID: 777,
						Variants: []contentapi.UploadCard{
							{
								VendorCode: "SKU-EXISTING",
							},
						},
					},
				},
			},
		},
	}

	obsZeroPhotos := CatalogObservation{
		Normal: []contentapi.Card{
			{
				VendorCode: "SKU-EXISTING",
				NMID:       999001,
				IMTID:      888001,
				SubjectID:  777,
				Photos:     nil,
			},
		},
	}

	decision, err := decideGroup(group, obsZeroPhotos)
	if err != nil {
		t.Fatalf("decideGroup error: %v", err)
	}
	if decision.productKind != "" {
		t.Fatalf("expected productKind to be empty, got %v", decision.productKind)
	}

	var draft PlanDraft
	if err := appendDecision(&draft, group, decision, Digest{2}); err != nil {
		t.Fatalf("appendDecision error: %v", err)
	}
	if len(draft.Actions) != 1 {
		t.Fatalf("expected 1 media action, got %d", len(draft.Actions))
	}
	if draft.Actions[0].Kind != ActionUploadMedia {
		t.Fatalf("expected ActionUploadMedia, got %v", draft.Actions[0].Kind)
	}
	if len(draft.Groups) != 1 || !draft.Groups[0].HasMediaAction {
		t.Fatalf("expected group HasMediaAction = true")
	}
	if draft.Groups[0].Status != transfer_service.PublicationProjectionSkipped {
		t.Fatalf("expected group status = skipped, got %v", draft.Groups[0].Status)
	}

	// Verify batchCreateGroupActions doesn't fail on standalone media actions
	if err := batchCreateGroupActions(&draft); err != nil {
		t.Fatalf("batchCreateGroupActions error on standalone media action: %v", err)
	}

	// Test existing card WITH photos does NOT generate media action
	obsWithPhotos := CatalogObservation{
		Normal: []contentapi.Card{
			{
				VendorCode: "SKU-EXISTING",
				NMID:       999001,
				IMTID:      888001,
				SubjectID:  777,
				Photos:     []contentapi.CardPhoto{{Big: "https://wb.ru/photo.jpg"}},
			},
		},
	}
	decision2, err := decideGroup(group, obsWithPhotos)
	if err != nil {
		t.Fatalf("decideGroup error: %v", err)
	}
	var draft2 PlanDraft
	if err := appendDecision(&draft2, group, decision2, Digest{2}); err != nil {
		t.Fatalf("appendDecision error: %v", err)
	}
	if len(draft2.Actions) != 0 {
		t.Fatalf("expected 0 actions for card with photos, got %d", len(draft2.Actions))
	}
	if len(draft2.Groups) != 1 || draft2.Groups[0].HasMediaAction {
		t.Fatalf("expected group HasMediaAction = false")
	}
}
