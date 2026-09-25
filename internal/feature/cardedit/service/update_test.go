package cardedit_service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

func TestBuildUpdatePreservesUnsafeFieldsAndOverlaysEditableValues(t *testing.T) {
	t.Parallel()

	gateway := &gatewayStub{characteristics: contentapi.SubjectCharacteristicsResponse{
		Data: []contentapi.SubjectCharacteristic{
			{CharacteristicID: 10, Name: "Цвет", CharacteristicType: 1, MaxCount: 3},
			{CharacteristicID: 20, Name: "Материал", CharacteristicType: 1, MaxCount: 2},
			{CharacteristicID: 30, Name: "Ставка НДС", CharacteristicType: 1, MaxCount: 1},
		},
	}}
	service := &Service{gateway: gateway}
	current := contentapi.Card{
		NMID:        123,
		SubjectID:   77,
		SubjectName: "Свитеры",
		VendorCode:  "seller-1",
		KIZMarked:   false,
		Characteristics: []contentapi.CardCharacteristic{
			{ID: 10, Name: "Цвет", Value: []any{"красный"}},
			{ID: 20, Name: "Материал", Value: []any{"хлопок"}},
		},
		Sizes: []contentapi.CardSize{{
			CHRTID: 456, TechSize: "M", WBSize: "46", SKUs: []string{"barcode-1"},
		}},
	}
	edited := cardpipeline.Card{
		MarkingConfirmed: "да",
		VATRate:          "20",
		Variant: cardpipeline.Variant{
			VendorCode:  "seller-1",
			Title:       "Новый заголовок",
			Description: "Новое описание",
			Brand:       "Новый бренд",
			Dimensions: cardpipeline.Dimensions{
				Length: 11, Width: 12, Height: 13, WeightBrutto: 1.5,
			},
			Sizes: []cardpipeline.Size{{SKUs: []string{"must-not-be-used"}}},
			Characteristics: []cardpipeline.RawCharacteristic{{
				Name: " цвет ", Value: "синий; зелёный",
			}},
		},
		Media: cardpipeline.Media{
			Photos: []string{"https://example.test/one.jpg"},
			Video:  "https://example.test/video.mp4",
		},
	}

	request, media, err := service.buildUpdate(
		context.Background(),
		Target{Cabinet: Cabinet{ID: "cabinet-1"}, NMID: current.NMID},
		current,
		edited,
	)
	if err != nil {
		t.Fatalf("build update: %v", err)
	}
	if !request.KIZMarked {
		t.Fatal("expected KIZ marking confirmation to be updated")
	}
	if request.Title != edited.Variant.Title || request.Description != edited.Variant.Description || request.Brand != edited.Variant.Brand {
		t.Fatalf("editable fields differ: %#v", request)
	}
	if len(request.Sizes) != 1 || request.Sizes[0].CHRTID != 456 || !reflect.DeepEqual(request.Sizes[0].SKUs, []string{"barcode-1"}) {
		t.Fatalf("current WB sizes were not preserved: %#v", request.Sizes)
	}
	characteristics := make(map[int64]any, len(request.Characteristics))
	for _, characteristic := range request.Characteristics {
		characteristics[characteristic.ID] = characteristic.Value
	}
	if !reflect.DeepEqual(characteristics[10], []string{"синий", "зелёный"}) {
		t.Fatalf("color was not overlaid: %#v", characteristics[10])
	}
	if !reflect.DeepEqual(characteristics[20], []any{"хлопок"}) {
		t.Fatalf("untouched characteristic was not preserved: %#v", characteristics[20])
	}
	if !reflect.DeepEqual(characteristics[30], []string{"20"}) {
		t.Fatalf("VAT was not overlaid: %#v", characteristics[30])
	}
	if !reflect.DeepEqual(media, []string{
		"https://example.test/one.jpg",
		"https://example.test/video.mp4",
	}) {
		t.Fatalf("media differs: %#v", media)
	}
}

func TestBuildUpdateRejectsMissingCurrentSizes(t *testing.T) {
	t.Parallel()

	service := &Service{gateway: &gatewayStub{}}
	_, _, err := service.buildUpdate(
		context.Background(),
		Target{Cabinet: Cabinet{ID: "cabinet-1"}, NMID: 123},
		contentapi.Card{NMID: 123, SubjectID: 77},
		cardpipeline.Card{},
	)
	if !errors.Is(err, ErrUnsafeUpdate) {
		t.Fatalf("expected unsafe update error, got %v", err)
	}
}

func TestRecentWBErrorsIgnoresOldBatches(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().UTC()
	service := &Service{gateway: &gatewayStub{errorsList: contentapi.CardsErrorListResponse{
		Data: contentapi.CardsErrorListData{Items: []contentapi.CardsErrorBatch{
			{UpdatedAt: startedAt.Add(-time.Hour).Format(time.RFC3339Nano), Errors: map[string][]string{"sku": {"old"}}},
			{UpdatedAt: startedAt.Add(time.Second).Format(time.RFC3339Nano), Errors: map[string][]string{"SKU": {"new"}}},
		}},
	}}}

	got, err := service.recentWBErrors(context.Background(), "cabinet-1", "sku", startedAt)
	if err != nil {
		t.Fatalf("recent WB errors: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("unexpected errors: %#v", got)
	}
}

type gatewayStub struct {
	cabinets        []Cabinet
	cards           map[domain.CabinetID][]contentapi.Card
	characteristics contentapi.SubjectCharacteristicsResponse
	errorsList      contentapi.CardsErrorListResponse
}

func (stub *gatewayStub) Cabinets(context.Context, int64) ([]Cabinet, error) {
	return append([]Cabinet(nil), stub.cabinets...), nil
}

func (stub *gatewayStub) CardsList(
	_ context.Context,
	cabinetID domain.CabinetID,
	_ contentapi.CardsListQuery,
	_ contentapi.CardsListRequest,
) (contentapi.CardsListResponse, error) {
	return contentapi.CardsListResponse{Cards: append([]contentapi.Card(nil), stub.cards[cabinetID]...)}, nil
}

func (stub *gatewayStub) SubjectCharacteristics(
	context.Context,
	domain.CabinetID,
	int64,
	contentapi.SubjectCharacteristicsQuery,
) (contentapi.SubjectCharacteristicsResponse, error) {
	return stub.characteristics, nil
}

func (stub *gatewayStub) CardsErrorList(
	context.Context,
	domain.CabinetID,
	contentapi.CardsErrorListQuery,
	contentapi.CardsErrorListRequest,
) (contentapi.CardsErrorListResponse, error) {
	return stub.errorsList, nil
}

func (*gatewayStub) UpdateCards(
	context.Context,
	domain.CabinetID,
	domain.ClientGeneration,
	contentapi.UpdateCardsRequest,
) (contentapi.UpdateCardsResponse, error) {
	return contentapi.UpdateCardsResponse{}, nil
}

func (*gatewayStub) SaveMediaByLinks(
	context.Context,
	domain.CabinetID,
	domain.ClientGeneration,
	contentapi.SaveMediaByLinksRequest,
) (contentapi.SaveMediaByLinksResponse, error) {
	return contentapi.SaveMediaByLinksResponse{}, nil
}
