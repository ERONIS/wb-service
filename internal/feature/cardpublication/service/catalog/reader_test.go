package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/observability"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer_service "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

	core, logs := observer.New(zapcore.InfoLevel)
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{TransferID: 42, ActionID: 73})
	observation, err := NewCatalogReader(stub, zap.New(core)).ReadVendorCodes(
		ctx,
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
	entries := logs.FilterField(zap.String("event", observability.TimingEvent)).All()
	if len(entries) != 1 {
		t.Fatalf("expected one catalog completion timing, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["transfer_id"] != int64(42) || fields["action_id"] != int64(73) ||
		fields["normal_reads_count"] != int64(1) || fields["result"] != "success" {
		t.Fatalf("missing catalog work/correlation fields: %v", fields)
	}
	if _, exists := fields["normal_reads_duration"]; !exists {
		t.Fatal("catalog timing is missing active-card read duration")
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

func TestCatalogReaderCachesFreshObservationOfOldCard(t *testing.T) {
	stub := &catalogTransportStub{}
	stub.cardsList = func(contentapi.CardsListRequest) contentapi.CardsListResponse {
		return contentapi.CardsListResponse{Cards: []contentapi.Card{{
			NMID:       102,
			VendorCode: "OLD-CARD",
			UpdatedAt:  time.Now().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano),
		}}}
	}
	reader := NewCatalogReader(stub)
	for range 2 {
		if _, err := reader.ReadActiveVendorCode(
			context.Background(),
			1,
			"main",
			"OLD-CARD",
		); err != nil {
			t.Fatal(err)
		}
	}
	if len(stub.cardsRequests) != 1 {
		t.Fatalf("fresh observation was tied to remote update age: %d", len(stub.cardsRequests))
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

func TestCatalogReaderScansSmallCatalogOnceForManyVendorCodes(t *testing.T) {
	stub := &catalogTransportStub{}
	stub.cardsList = func(request contentapi.CardsListRequest) contentapi.CardsListResponse {
		return contentapi.CardsListResponse{
			Cards: []contentapi.Card{
				{NMID: 1, VendorCode: "SKU-001"},
				{NMID: 2, VendorCode: "UNRELATED"},
			},
			Cursor: contentapi.CardsResponseCursor{Total: 2},
		}
	}
	stub.trashList = func(request contentapi.TrashCardsListRequest) contentapi.TrashCardsListResponse {
		return contentapi.TrashCardsListResponse{
			Cards:  []contentapi.TrashCard{{NMID: 3, VendorCode: "SKU-002"}},
			Cursor: contentapi.TrashCardsResponseCursor{Total: 1},
		}
	}
	vendorCodes := make([]string, bulkVendorCodeScanThreshold)
	for index := range vendorCodes {
		vendorCodes[index] = fmt.Sprintf("SKU-%03d", index+1)
	}

	observation, err := NewCatalogReader(stub).ReadVendorCodes(
		context.Background(),
		1,
		"main",
		vendorCodes,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.cardsRequests) != 1 || len(stub.trashRequests) != 1 {
		t.Fatalf(
			"large vendor set used exact searches: normal=%d trash=%d",
			len(stub.cardsRequests),
			len(stub.trashRequests),
		)
	}
	if stub.cardsRequests[0].Settings.Filter != nil ||
		stub.trashRequests[0].Settings.Filter != nil {
		t.Fatal("bulk scan unexpectedly used a text filter")
	}
	if len(observation.Normal) != 1 || observation.Normal[0].NMID != 1 ||
		len(observation.Trash) != 1 || observation.Trash[0].NMID != 3 {
		t.Fatalf("unexpected bulk observation: %#v", observation)
	}
}

func TestCatalogReaderFallsBackWhenCatalogScanCostsMore(t *testing.T) {
	stub := &catalogTransportStub{}
	stub.cardsList = func(request contentapi.CardsListRequest) contentapi.CardsListResponse {
		if request.Settings.Filter == nil {
			cards := make([]contentapi.Card, contentapi.MaxCardsListPageSize)
			return contentapi.CardsListResponse{
				Cards: cards,
				Cursor: contentapi.CardsResponseCursor{
					UpdatedAt: "next",
					NMID:      request.Settings.Cursor.NMID + 1,
					Total:     contentapi.MaxCardsListPageSize,
				},
			}
		}
		return contentapi.CardsListResponse{}
	}
	stub.trashList = func(contentapi.TrashCardsListRequest) contentapi.TrashCardsListResponse {
		return contentapi.TrashCardsListResponse{}
	}
	vendorCodes := make([]string, bulkVendorCodeScanThreshold)
	for index := range vendorCodes {
		vendorCodes[index] = fmt.Sprintf("SKU-%03d", index+1)
	}

	if _, err := NewCatalogReader(stub).ReadVendorCodes(
		context.Background(),
		1,
		"main",
		vendorCodes,
	); err != nil {
		t.Fatal(err)
	}
	if len(stub.cardsRequests) != len(vendorCodes)+normalCatalogProbePages {
		t.Fatalf("unexpected fallback request count: %d", len(stub.cardsRequests))
	}
	if stub.cardsRequests[0].Settings.Filter != nil {
		t.Fatal("catalog probe unexpectedly used a text filter")
	}
	for _, request := range stub.cardsRequests[normalCatalogProbePages:] {
		if request.Settings.Filter == nil || request.Settings.Filter.TextSearch == "" {
			t.Fatal("fallback did not use exact vendor-code search")
		}
	}
}

func TestCatalogReaderContinuesAfterFullPageWithPageTotal(t *testing.T) {
	stub := &catalogTransportStub{}
	cardsCalls := 0
	stub.cardsList = func(contentapi.CardsListRequest) contentapi.CardsListResponse {
		cardsCalls++
		return contentapi.CardsListResponse{
			Cards: []contentapi.Card{{NMID: 77, VendorCode: "SKU-000"}},
			Cursor: contentapi.CardsResponseCursor{
				UpdatedAt: fmt.Sprintf("page-%d", cardsCalls),
				NMID:      int64(cardsCalls),
				Total:     1,
			},
		}
	}
	trashCalls := 0
	stub.trashList = func(contentapi.TrashCardsListRequest) contentapi.TrashCardsListResponse {
		trashCalls++
		cards := make([]contentapi.TrashCard, contentapi.MaxTrashCardsListPageSize)
		if trashCalls == 2 {
			cards = []contentapi.TrashCard{{NMID: 78, VendorCode: "SKU-000"}}
		}
		return contentapi.TrashCardsListResponse{Cards: cards, Cursor: contentapi.TrashCardsResponseCursor{
			TrashedAt: fmt.Sprintf("page-%d", trashCalls), NMID: int64(trashCalls), Total: len(cards),
		}}
	}
	vendorCodes := make([]string, bulkVendorCodeScanThreshold)
	for index := range vendorCodes {
		vendorCodes[index] = fmt.Sprintf("SKU-%03d", index)
	}

	observation, err := NewCatalogReader(stub).ReadVendorCodes(
		context.Background(),
		1,
		"main",
		vendorCodes,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cardsCalls != 1 || trashCalls != 2 || len(observation.Normal) != 1 || len(observation.Trash) != 1 {
		t.Fatalf("page total hid later cards: normal_calls=%d trash_calls=%d observation=%+v", cardsCalls, trashCalls, observation)
	}
	for _, request := range stub.cardsRequests {
		if request.Settings.Filter != nil {
			t.Fatal("completed bulk scan unexpectedly fell back to text search")
		}
	}
}

var _ CatalogTransport = (*catalogTransportStub)(nil)

type deadlineTransport struct {
	CatalogTransport
	calls int
}

func (s *deadlineTransport) CardsList(ctx context.Context, _ CabinetID, _ contentapi.CardsListQuery, _ contentapi.CardsListRequest) (contentapi.CardsListResponse, error) {
	s.calls++
	<-ctx.Done()
	return contentapi.CardsListResponse{}, ctx.Err()
}
func TestMediaTimeoutBoundsInFlightRead(t *testing.T) {
	transport := &deadlineTransport{}
	reader := NewCatalogReader(transport)
	// The outer deadline prevents a broken implementation from hanging the test.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err := reader.WaitForMedia(ctx, "one", "SKU", 1, 1, false, time.Millisecond, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond || ctx.Err() != nil {
		t.Fatal("media deadline did not reach in-flight read")
	}
	if transport.calls != 1 {
		t.Fatalf("repeated expired request: %d", transport.calls)
	}
}
func TestMediaCancellationDoesNotStartRead(t *testing.T) {
	transport := &deadlineTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewCatalogReader(transport).WaitForMedia(ctx, "one", "SKU", 1, 1, false, time.Millisecond, time.Second)
	if !errors.Is(err, context.Canceled) || transport.calls != 0 {
		t.Fatalf("canceled read dispatched: %v calls=%d", err, transport.calls)
	}
}
func TestMediaTimeoutReportsLastObservedPhotos(t *testing.T) {
	transport := &catalogTransportStub{cardsList: func(contentapi.CardsListRequest) contentapi.CardsListResponse {
		return contentapi.CardsListResponse{Cards: []contentapi.Card{{NMID: 1, VendorCode: "SKU", Photos: []contentapi.CardPhoto{{Big: "https://example.test/photo.jpg"}}}}}
	}}
	core, logs := observer.New(zapcore.DebugLevel)
	err := NewCatalogReader(transport, zap.New(core)).WaitForMedia(context.Background(), "one", "SKU", 1, 2, false, time.Millisecond, 25*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected missing-media timeout: %v", err)
	}
	entries := logs.FilterMessage("Timed operation completed").All()
	if len(entries) != 1 {
		t.Fatalf("missing diagnostic: %v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["last_card_found"] != true || fields["last_photos_count"] != int64(1) || fields["expected_photos_count"] != int64(2) {
		t.Fatalf("missing observed count: %v", fields)
	}
	if len(transport.cardsRequests) > 7 {
		t.Fatalf("polling did not back off: %d requests", len(transport.cardsRequests))
	}
}
