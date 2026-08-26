package cardpublication_service

import (
	"context"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

type catalogTransportStub struct {
	cardsRequests []contentapi.CardsListRequest
	trashRequests []contentapi.TrashCardsListRequest
	cardsList     func(contentapi.CardsListRequest) contentapi.CardsListResponse
	trashList     func(contentapi.TrashCardsListRequest) contentapi.TrashCardsListResponse
}

func (stub *catalogTransportStub) CardsList(
	_ context.Context,
	_ CabinetID,
	_ contentapi.CardsListQuery,
	request contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	stub.cardsRequests = append(stub.cardsRequests, request)
	if stub.cardsList == nil {
		return contentapi.CardsListResponse{}, nil
	}
	return stub.cardsList(request), nil
}

func (stub *catalogTransportStub) TrashCardsList(
	_ context.Context,
	_ CabinetID,
	_ contentapi.TrashCardsListQuery,
	request contentapi.TrashCardsListRequest,
) (contentapi.TrashCardsListResponse, error) {
	stub.trashRequests = append(stub.trashRequests, request)
	if stub.trashList == nil {
		return contentapi.TrashCardsListResponse{}, nil
	}
	return stub.trashList(request), nil
}

func (*catalogTransportStub) CardsErrorList(
	context.Context,
	CabinetID,
	contentapi.CardsErrorListQuery,
	contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return contentapi.CardsErrorListResponse{}, nil
}

func (*catalogTransportStub) UploadCards(
	context.Context,
	CabinetID,
	transfer_service.ClientGeneration,
	contentapi.UploadCardsRequest,
) (contentapi.UploadCardsResponse, error) {
	return contentapi.UploadCardsResponse{}, nil
}

func (*catalogTransportStub) UploadCardsAdd(
	context.Context,
	CabinetID,
	transfer_service.ClientGeneration,
	contentapi.UploadCardsAddRequest,
) (contentapi.UploadCardsAddResponse, error) {
	return contentapi.UploadCardsAddResponse{}, nil
}

func (*catalogTransportStub) SaveMediaByLinks(
	context.Context,
	CabinetID,
	transfer_service.ClientGeneration,
	contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return contentapi.SaveMediaByLinksResponse{}, nil
}

func TestCatalogReaderReadVendorCodesUsesTextSearchAndExactMatch(t *testing.T) {
	stub := &catalogTransportStub{}
	stub.cardsList = func(request contentapi.CardsListRequest) contentapi.CardsListResponse {
		return contentapi.CardsListResponse{Cards: []contentapi.Card{
			{NMID: 11, VendorCode: "SKU-1"},
			{NMID: 12, VendorCode: "SKU-10"},
		}}
	}
	stub.trashList = func(request contentapi.TrashCardsListRequest) contentapi.TrashCardsListResponse {
		return contentapi.TrashCardsListResponse{Cards: []contentapi.TrashCard{
			{NMID: 21, VendorCode: "SKU-1"},
			{NMID: 22, VendorCode: "SKU-100"},
		}}
	}

	observation, err := NewCatalogReader(stub).ReadVendorCodes(
		context.Background(),
		1,
		"main",
		[]string{"SKU-1"},
	)
	if err != nil {
		t.Fatalf("read targeted catalog: %v", err)
	}
	if len(stub.cardsRequests) != 1 || stub.cardsRequests[0].Settings.Filter == nil ||
		stub.cardsRequests[0].Settings.Filter.TextSearch != "SKU-1" {
		t.Fatalf("active catalog was not searched by vendorCode: %#v", stub.cardsRequests)
	}
	if len(stub.trashRequests) != 1 || stub.trashRequests[0].Settings.Filter == nil ||
		stub.trashRequests[0].Settings.Filter.TextSearch != "SKU-1" {
		t.Fatalf("trash was not searched by vendorCode: %#v", stub.trashRequests)
	}
	if len(observation.Normal) != 1 || observation.Normal[0].NMID != 11 {
		t.Fatalf("unexpected exact active matches: %#v", observation.Normal)
	}
	if len(observation.Trash) != 1 || observation.Trash[0].NMID != 21 {
		t.Fatalf("unexpected exact trash matches: %#v", observation.Trash)
	}
}

func TestCatalogReaderCachesRecentExactVendorCodeObservation(t *testing.T) {
	now := time.Now().UTC()
	stub := &catalogTransportStub{}
	stub.cardsList = func(request contentapi.CardsListRequest) contentapi.CardsListResponse {
		return contentapi.CardsListResponse{Cards: []contentapi.Card{
			{
				NMID:       101,
				VendorCode: "RECENT",
				UpdatedAt:  now.Add(-30 * time.Minute).Format(time.RFC3339Nano),
				Photos:     []contentapi.CardPhoto{{Big: "https://example.test/photo.jpg"}},
			},
		}}
	}
	reader := NewCatalogReader(stub)
	if _, err := reader.ReadVendorCodes(context.Background(), 1, "main", []string{"RECENT"}); err != nil {
		t.Fatalf("read recent card: %v", err)
	}
	if len(stub.cardsRequests) != 1 {
		t.Fatalf("unexpected exact-search request count: %d", len(stub.cardsRequests))
	}
	filter := stub.cardsRequests[0].Settings.Filter
	if filter == nil || filter.TextSearch != "RECENT" {
		t.Fatalf("unexpected exact-search filter: %#v", filter)
	}
	card, ok := reader.recent.LookupVendorCode("main", " RECENT ")
	if !ok || card.NMID != 101 {
		t.Fatalf("recent nmID was not cached: card=%#v ok=%v", card, ok)
	}
	photos, video, ok := reader.recent.LookupMedia("main", 101)
	if !ok || photos != 1 || video {
		t.Fatalf("recent media was not cached: photos=%d video=%v ok=%v", photos, video, ok)
	}
	if _, err := reader.ReadVendorCodes(context.Background(), 1, "main", []string{"RECENT"}); err != nil {
		t.Fatalf("read cached card: %v", err)
	}
	if len(stub.cardsRequests) != 1 {
		t.Fatalf("active API was called after exact cache fill: %d", len(stub.cardsRequests))
	}
}

func TestCatalogReaderWaitForMediaRefreshesOnlyExactVendorCode(t *testing.T) {
	now := time.Now().UTC()
	stub := &catalogTransportStub{}
	calls := 0
	stub.cardsList = func(request contentapi.CardsListRequest) contentapi.CardsListResponse {
		calls++
		card := contentapi.Card{
			NMID:       101,
			VendorCode: "RECENT",
			UpdatedAt:  now.Format(time.RFC3339Nano),
		}
		if calls >= 2 {
			card.Photos = []contentapi.CardPhoto{{Big: "https://example.test/photo.jpg"}}
		}
		return contentapi.CardsListResponse{Cards: []contentapi.Card{card}}
	}
	reader := NewCatalogReader(stub)
	if err := reader.WaitForMedia(
		context.Background(), "main", "RECENT", 101, 1, false,
		time.Millisecond, time.Second,
	); err != nil {
		t.Fatalf("wait for exact media: %v", err)
	}
	if calls != 2 {
		t.Fatalf("unexpected media refresh calls: got %d, want 2", calls)
	}
	for _, request := range stub.cardsRequests {
		if request.Settings.Filter == nil || request.Settings.Filter.TextSearch != "RECENT" {
			t.Fatalf("media refresh did not use exact vendorCode: %#v", request)
		}
	}
	if len(stub.trashRequests) != 0 {
		t.Fatalf("media refresh must not query trash: %d", len(stub.trashRequests))
	}
}

func TestCatalogReaderUsesRecentCacheForActiveCard(t *testing.T) {
	now := time.Now().UTC()
	stub := &catalogTransportStub{}
	reader := NewCatalogReader(stub)
	reader.recent.StoreIfRecent("main", contentapi.Card{
		NMID:       501,
		VendorCode: "SKU-CACHED",
		UpdatedAt:  now.Format(time.RFC3339Nano),
	})

	observation, err := reader.ReadVendorCodes(
		context.Background(),
		1,
		"main",
		[]string{"SKU-CACHED"},
	)
	if err != nil {
		t.Fatalf("read cached vendor code: %v", err)
	}
	if len(stub.cardsRequests) != 0 {
		t.Fatalf("active WB API must not be called on cache hit: %d", len(stub.cardsRequests))
	}
	if len(stub.trashRequests) != 1 {
		t.Fatalf("trash must still be checked by vendorCode: %d", len(stub.trashRequests))
	}
	if len(observation.Normal) != 1 || observation.Normal[0].NMID != 501 {
		t.Fatalf("unexpected cached observation: %#v", observation.Normal)
	}
}

var _ CatalogTransport = (*catalogTransportStub)(nil)
