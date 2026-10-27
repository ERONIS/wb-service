package planning

import (
	"encoding/json"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_model "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/model"
)

func TestBatchCreateGroupActionsKeepsItemReferences(t *testing.T) {
	draft := cardpublication_model.PlanDraft{Actions: make([]cardpublication_model.ActionDraft, 0, 3)}
	for index, position := range []int{1, 3} {
		request, err := json.Marshal(contentapi.UploadCardsRequest{{
			SubjectID: 1,
			Variants:  []contentapi.UploadCard{{VendorCode: string(rune('A' + index))}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		member := cardpublication_model.ActionMemberDraft{
			GroupTargetID:        int64(index + 1),
			TransferItemTargetID: int64(index + 1),
			VendorCode:           string(rune('A' + index)),
		}
		draft.Actions = append(draft.Actions, cardpublication_model.ActionDraft{
			Position:       position,
			Kind:           cardpublication_model.ActionCreateGroup,
			TargetID:       1,
			RequestPayload: request,
			Members:        []cardpublication_model.ActionMemberDraft{member},
		})
		draft.Items = append(draft.Items, cardpublication_model.PlannedItemDraft{
			GroupTargetID:        member.GroupTargetID,
			TransferItemTargetID: member.TransferItemTargetID,
			ActionPosition:       position,
		})
	}
	draft.Actions = append(draft.Actions, cardpublication_model.ActionDraft{
		Position:       2,
		Kind:           cardpublication_model.ActionUploadMedia,
		TargetID:       1,
		RequestPayload: []byte(`{"links":["https://example.com/a.jpg"]}`),
		Members:        []cardpublication_model.ActionMemberDraft{{GroupTargetID: 1, TransferItemTargetID: 1, VendorCode: "A"}},
	})

	if err := batchCreateGroupActions(&draft); err != nil {
		t.Fatal(err)
	}
	if len(draft.Actions) != 2 {
		t.Fatalf("ожидалось 2 действия, получено %d", len(draft.Actions))
	}
	for _, item := range draft.Items {
		if item.ActionPosition != 1 {
			t.Fatalf("карточка %d связана с действием %d", item.TransferItemTargetID, item.ActionPosition)
		}
	}
	for _, action := range draft.Actions {
		if action.Kind == cardpublication_model.ActionCreateGroup && len(action.Members) != 2 {
			t.Fatalf("после объединения ожидалось 2 карточки, получено %d", len(action.Members))
		}
	}
}
