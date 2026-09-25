package cabinetcopy_wb_transport

import (
	"context"
	"reflect"
	"testing"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
)

type executorFunc func(context.Context, policy.Operation, any, any, any) error

func (function executorFunc) Execute(ctx context.Context, operation policy.Operation, query, body, target any) error {
	return function(ctx, operation, query, body, target)
}

type readerClientsetStub struct {
	content client.Executor
	prices  client.Executor
}

func (stub readerClientsetStub) Cabinets() []core_wb.CabinetInfo {
	return []core_wb.CabinetInfo{{ID: "source", Name: "Source"}}
}

func (stub readerClientsetStub) ExecutorForCabinet(wbconfig.CabinetID) (client.Executor, error) {
	return stub.content, nil
}

func (stub readerClientsetStub) PricesExecutorForCabinet(wbconfig.CabinetID) (client.Executor, error) {
	return stub.prices, nil
}

func TestReadCardsPassesTagFilterAndMapsPerSizePrices(t *testing.T) {
	var receivedTags []int64
	content := executorFunc(func(_ context.Context, operation policy.Operation, _, body, target any) error {
		if operation.ID() != contentapi.CardsListOperation().ID() {
			t.Fatalf("content operation = %s", operation.ID())
		}
		request := body.(contentapi.CardsListRequest)
		receivedTags = append([]int64(nil), request.Settings.Filter.TagIDs...)
		response := target.(*contentapi.CardsListResponse)
		response.Cards = []contentapi.Card{{
			NMID:        100,
			IMTID:       200,
			SubjectID:   300,
			SubjectName: "Футболки",
			VendorCode:  "vendor-1",
			Title:       "Title",
			Description: "Description",
			Dimensions:  contentapi.CardDimensions{Length: 1, Width: 2, Height: 3, WeightBrutto: 0.5},
			Characteristics: []contentapi.CardCharacteristic{{
				ID: 10, Name: "Материал", Value: []any{"хлопок"},
			}},
			Sizes: []contentapi.CardSize{
				{CHRTID: 501, TechSize: "S", WBSize: "42"},
				{CHRTID: 502, TechSize: "M", WBSize: "44"},
			},
		}}
		response.Cursor.Total = 1
		return nil
	})
	prices := executorFunc(func(_ context.Context, operation policy.Operation, _, body, target any) error {
		if operation.ID() != pricesapi.GoodsByNMOperation().ID() {
			t.Fatalf("prices operation = %s", operation.ID())
		}
		request := body.(pricesapi.GoodsByNMRequest)
		if !reflect.DeepEqual(request.NMList, []int64{100}) {
			t.Fatalf("nmList = %v", request.NMList)
		}
		response := target.(*pricesapi.GoodsResponse)
		response.Data.ListGoods = []pricesapi.Good{{
			NMID: 100,
			Sizes: []pricesapi.GoodSize{
				{SizeID: 501, Price: 1100},
				{SizeID: 502, Price: 1200},
			},
		}}
		return nil
	})
	reader := NewReader(readerClientsetStub{content: content, prices: prices})

	items, err := reader.ReadCards(context.Background(), cabinetcopy_service.CabinetID("source"), []int64{7, 9}, 1)
	if err != nil {
		t.Fatalf("ReadCards() error = %v", err)
	}
	if !reflect.DeepEqual(receivedTags, []int64{7, 9}) {
		t.Fatalf("tagIDs = %v", receivedTags)
	}
	if len(items) != 1 || len(items[0].Card.Variant.Sizes) != 2 {
		t.Fatalf("items = %#v", items)
	}
	sizes := items[0].Card.Variant.Sizes
	if sizes[0].Price != 1100 || sizes[1].Price != 1200 {
		t.Fatalf("size prices = %d/%d", sizes[0].Price, sizes[1].Price)
	}
	if len(sizes[0].SKUs) != 0 || items[0].Card.Group != "wb-imt:200" {
		t.Fatalf("mapped card = %#v", items[0].Card)
	}
	if characteristics := items[0].Card.Variant.Characteristics; len(characteristics) != 1 || characteristics[0].ID != 10 {
		t.Fatalf("characteristics = %#v", characteristics)
	}
}
