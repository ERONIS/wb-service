package cardpublication_service

import (
	"encoding/json"
	"fmt"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

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
			if !uploadVendorMatchesSubject(request, member.VendorCode, 777) {
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
