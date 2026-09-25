package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardpublication_catalog "github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type reconciliationCatalogTransport struct {
	CatalogTransport
	cardsRequests atomic.Int64
	trashRequests atomic.Int64
	cards         []contentapi.Card
}

func (transport *reconciliationCatalogTransport) CardsList(
	context.Context,
	CabinetID,
	contentapi.CardsListQuery,
	contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	transport.cardsRequests.Add(1)
	return contentapi.CardsListResponse{Cards: transport.cards}, nil
}

func (transport *reconciliationCatalogTransport) TrashCardsList(
	context.Context,
	CabinetID,
	contentapi.TrashCardsListQuery,
	contentapi.TrashCardsListRequest,
) (contentapi.TrashCardsListResponse, error) {
	transport.trashRequests.Add(1)
	return contentapi.TrashCardsListResponse{}, nil
}

func TestReadReconciliationCatalogBatchesCandidatesByTarget(t *testing.T) {
	transport := &reconciliationCatalogTransport{}
	dispatcher := &ProductDispatcher{
		catalogReader: cardpublication_catalog.NewCatalogReader(transport),
		concurrency:   5,
	}
	candidates := make([]ProductActionCandidate, 20)
	for index := range candidates {
		vendorCode := fmt.Sprintf("SKU-%03d", index)
		transport.cards = append(transport.cards, contentapi.Card{
			NMID: int64(index + 1), VendorCode: vendorCode,
		})
		candidates[index] = ProductActionCandidate{
			TargetID:           11,
			CabinetID:          "main",
			CatalogVendorCodes: []string{vendorCode},
		}
	}

	observations, err := dispatcher.readProductCatalog(
		context.Background(),
		candidates,
	)
	if err != nil {
		t.Fatal(err)
	}
	observation, exists := observations[productCatalogKey{
		targetID: 11, cabinetID: "main",
	}]
	if !exists || len(observation.Normal) != len(candidates) {
		t.Fatalf("unexpected batched observation: %#v", observation)
	}
	if transport.cardsRequests.Load() != 1 || transport.trashRequests.Load() != 1 {
		t.Fatalf(
			"reconciliation candidates were read independently: normal=%d trash=%d",
			transport.cardsRequests.Load(),
			transport.trashRequests.Load(),
		)
	}
}

func TestReconcileProductEvidenceReturnsResolvedMembersBeforeActionCompletes(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	action := productReconciliationAction(t, now)

	results, complete, actionClass, actionCode := reconcileProductEvidence(
		action,
		CatalogObservation{Normal: []contentapi.Card{{
			NMID: 202, IMTID: 302, SubjectID: 777, VendorCode: "SKU-2",
		}}},
		nil,
		now,
		10*time.Minute,
	)

	if complete || actionClass != "" || actionCode != "" {
		t.Fatalf("action completed while one member is pending: complete=%t class=%q code=%q", complete, actionClass, actionCode)
	}
	if len(results) != 1 || results[0].ActionMemberID != 2 ||
		results[0].OutcomeClass != transfer_service.ResultSuccess || results[0].NMID != 202 {
		t.Fatalf("resolved member was not returned for persistence: %#v", results)
	}
}

func TestReconcileProductEvidenceUsesPersistedMemberProgress(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	action := productReconciliationAction(t, now)
	action.Members[0].OutcomeClass = transfer_service.ResultSuccess
	action.Members[0].OutcomeCode = "CREATED_CONFIRMED_BY_VENDOR_CODE"
	action.Members[0].NMID = 201

	if got := action.PendingVendorCodes(); len(got) != 1 || got[0] != "SKU-2" {
		t.Fatalf("unexpected pending vendor codes: %#v", got)
	}
	results, complete, actionClass, actionCode := reconcileProductEvidence(
		action,
		CatalogObservation{Normal: []contentapi.Card{{
			NMID: 202, IMTID: 302, SubjectID: 777, VendorCode: "SKU-2",
		}}},
		nil,
		now,
		10*time.Minute,
	)

	if !complete || actionClass != transfer_service.ResultSuccess ||
		actionCode != "PRODUCT_CREATED_BY_VENDOR_CODE" {
		t.Fatalf("unexpected completed action result: complete=%t class=%q code=%q", complete, actionClass, actionCode)
	}
	if len(results) != 1 || results[0].ActionMemberID != 2 {
		t.Fatalf("persisted member was reconciled again: %#v", results)
	}
	if err := ValidateMemberReconciliationResults(action, results); err != nil {
		t.Fatalf("validate remaining member result: %v", err)
	}
	if err := ValidateMemberReconciliationResults(action, []MemberReconciliationResult{{
		ActionMemberID:   1,
		OutcomeClass:     transfer_service.ResultSuccess,
		OutcomeCode:      "CREATED_CONFIRMED_BY_VENDOR_CODE",
		NMID:             201,
		IMTID:            301,
		SubjectID:        777,
		AttributionLevel: "observed_after_attempt",
	}}); err == nil {
		t.Fatal("accepted a second result for an already persisted member")
	}
}

func TestImmutableProductActionComparisonAllowsMemberProgress(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	before := productReconciliationAction(t, now)
	after := before
	after.Members = append([]ProductActionMember(nil), before.Members...)
	after.Members[0].OutcomeClass = transfer_service.ResultSuccess
	after.Members[0].OutcomeCode = "CREATED_CONFIRMED_BY_VENDOR_CODE"
	after.Members[0].NMID = 201

	if !sameImmutableProductAction(before, after) {
		t.Fatal("persisted member progress changed the immutable action identity")
	}
	if sameProductActionProgress(before, after) {
		t.Fatal("concurrent member progress was not detected")
	}
	before.Members[0].OutcomeClass = after.Members[0].OutcomeClass
	before.Members[0].OutcomeCode = after.Members[0].OutcomeCode
	before.Members[0].NMID = after.Members[0].NMID
	if !sameProductActionProgress(before, after) {
		t.Fatal("equal member progress was rejected")
	}
	after.Members[0].VendorCode = "CHANGED"
	if sameImmutableProductAction(before, after) {
		t.Fatal("changed member identity was accepted")
	}
}

func TestEmptyResponseRetryStartsOnlyAfterTenMinutesWithoutEvidence(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	action := productReconciliationAction(t, now)
	action.AttemptDelivery = SubmissionResponseReceived
	action.AttemptHTTPStatus = 200
	action.AttemptSafeCode = "WB_TRANSPORT_EMPTY_RESPONSE"
	action.AttemptStartedAt = now.Add(-emptyResponseProductRetryDelay)
	action.AttemptFinishedAt = action.AttemptStartedAt

	if !shouldRetryEmptyProductResponse(
		action,
		CatalogObservation{},
		nil,
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("empty response was not retried after the reconciliation delay")
	}
	action.AttemptFinishedAt = now.Add(-emptyResponseProductRetryDelay + time.Second)
	if shouldRetryEmptyProductResponse(
		action,
		CatalogObservation{},
		nil,
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("empty response was retried before ten minutes elapsed")
	}
}

func TestEmptyResponseRetryRequiresEveryMemberToRemainMissing(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	action := productReconciliationAction(t, now)
	action.AttemptDelivery = SubmissionResponseReceived
	action.AttemptHTTPStatus = 200
	action.AttemptSafeCode = "WB_TRANSPORT_EMPTY_RESPONSE"
	action.AttemptStartedAt = now.Add(-emptyResponseProductRetryDelay)
	action.AttemptFinishedAt = action.AttemptStartedAt

	withCard := CatalogObservation{Normal: []contentapi.Card{{
		NMID: 201, IMTID: 301, SubjectID: 777, VendorCode: "SKU-1",
	}}}
	if shouldRetryEmptyProductResponse(
		action,
		withCard,
		nil,
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("request was retried after WB returned a matching card")
	}
	if shouldRetryEmptyProductResponse(
		action,
		CatalogObservation{},
		[]ErrorBatchMatch{{RejectedVendorCodes: []string{"SKU-1"}}},
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("request was retried after WB returned rejection evidence")
	}
	action.Members[0].OutcomeClass = transfer_service.ResultSuccess
	action.Members[0].OutcomeCode = "CREATED_CONFIRMED_BY_VENDOR_CODE"
	action.Members[0].NMID = 201
	if shouldRetryEmptyProductResponse(
		action,
		CatalogObservation{},
		nil,
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("group request was retried after one member had already completed")
	}
}

func TestEmptyResponseRetryCanOnlyStartOnce(t *testing.T) {
	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	action := productReconciliationAction(t, now)
	action.AttemptDelivery = SubmissionResponseReceived
	action.AttemptHTTPStatus = 200
	action.AttemptSafeCode = "WB_TRANSPORT_EMPTY_RESPONSE"
	action.AttemptStartedAt = now.Add(-emptyResponseProductRetryDelay)
	action.AttemptFinishedAt = action.AttemptStartedAt
	action.EmptyResponseRetry = ProductRetry{ID: 7, StartedAt: now.Add(-time.Minute)}

	if shouldRetryEmptyProductResponse(
		action,
		CatalogObservation{},
		nil,
		now,
		emptyResponseProductRetryDelay,
	) {
		t.Fatal("a second empty-response retry was allowed")
	}
}

func TestRejectedEmptyResponseRetryProducesTerminalMemberResults(t *testing.T) {
	action := productReconciliationAction(t, time.Now().UTC())
	action.EmptyResponseRetry = ProductRetry{
		ID:          7,
		FinishedAt:  time.Now().UTC(),
		Delivery:    SubmissionResponseReceived,
		Disposition: SubmissionRejectedProven,
	}
	if !retryResponseProvedRejection(action.EmptyResponseRetry) {
		t.Fatal("proven retry rejection was not recognized")
	}
	results := rejectedRetryResults(action)
	if len(results) != len(action.Members) {
		t.Fatalf("rejected members = %d, want %d", len(results), len(action.Members))
	}
	for _, result := range results {
		if result.OutcomeClass != transfer_service.ResultRejected || result.OutcomeCode == "" {
			t.Fatalf("unexpected rejected retry result: %#v", result)
		}
	}
}

func productReconciliationAction(t *testing.T, now time.Time) ProductAction {
	t.Helper()
	payload, err := json.Marshal(contentapi.UploadCardsRequest{{
		SubjectID: 777,
		Variants: []contentapi.UploadCard{
			{VendorCode: "SKU-1"},
			{VendorCode: "SKU-2"},
		},
	}})
	if err != nil {
		t.Fatalf("encode product request: %v", err)
	}
	return ProductAction{
		Kind:             ActionCreateGroup,
		RequestPayload:   payload,
		AttemptStartedAt: now.Add(-time.Minute),
		Members: []ProductActionMember{
			{ID: 1, GroupTargetID: 11, TransferItemTargetID: 101, RequestMemberIndex: 0, VendorCode: "SKU-1"},
			{ID: 2, GroupTargetID: 11, TransferItemTargetID: 102, RequestMemberIndex: 1, VendorCode: "SKU-2"},
		},
	}
}
