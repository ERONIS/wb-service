package cabinetcopy_wb_transport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	wb_catalog "github.com/ERONIS/wb-service/internal/core/transport/wb/catalog"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

const maxPriceBatchSize = 1000

type ContentClient interface {
	wb_catalog.CardsLister
	Tags(context.Context, domain.CabinetID) (contentapi.TagsResponse, error)
}
type PricesClient interface {
	GoodsByNM(context.Context, domain.CabinetID, pricesapi.GoodsByNMRequest) (pricesapi.GoodsResponse, error)
}

type CabinetSource interface {
	Targets(context.Context, int64) ([]wbcabinet_service.TargetCredential, error)
}

type Reader struct {
	content  ContentClient
	prices   PricesClient
	cabinets CabinetSource
}

func NewReader(content ContentClient, prices PricesClient, cabinets CabinetSource) *Reader {
	if content == nil || prices == nil || cabinets == nil {
		panic("cabinet copy dependencies are nil")
	}
	return &Reader{content: content, prices: prices, cabinets: cabinets}
}

func (reader *Reader) CabinetsForOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]cabinetcopy_service.Cabinet, error) {
	targets, err := reader.cabinets.Targets(ctx, ownerTelegramID)
	if err != nil {
		return nil, fmt.Errorf("list owned WB cabinets: %w", err)
	}
	result := make([]cabinetcopy_service.Cabinet, len(targets))
	for index, target := range targets {
		result[index] = cabinetcopy_service.Cabinet{
			ID:   cabinetcopy_service.CabinetID(target.CabinetID),
			Name: target.Name,
		}
	}
	return result, nil
}

func (reader *Reader) ListTags(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
) ([]cabinetcopy_service.Tag, error) {
	response, err := reader.content.Tags(ctx, cabinetID)
	if err != nil {
		return nil, fmt.Errorf("get WB cabinet tags: %w", err)
	}
	if response.Error {
		return nil, fmt.Errorf("WB tags response: %s", strings.TrimSpace(response.ErrorText))
	}
	result := make([]cabinetcopy_service.Tag, 0, len(response.Data))
	seen := make(map[int64]struct{}, len(response.Data))
	for _, tag := range response.Data {
		if tag.ID <= 0 || strings.TrimSpace(tag.Name) == "" {
			return nil, errors.New("WB tags response contains invalid tag")
		}
		if _, exists := seen[tag.ID]; exists {
			return nil, errors.New("WB tags response contains duplicate tag")
		}
		seen[tag.ID] = struct{}{}
		result = append(result, cabinetcopy_service.Tag{
			ID: tag.ID, Name: strings.TrimSpace(tag.Name), Color: strings.TrimSpace(tag.Color),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (reader *Reader) ReadCards(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
	tagIDs []int64,
	count int,
) ([]cabinetcopy_service.PreparedItem, error) {
	if count <= 0 {
		return nil, errors.New("cabinet copy card count must be positive")
	}
	cards, err := reader.readContentCards(ctx, cabinetID, tagIDs, count)
	if err != nil {
		return nil, err
	}
	if len(cards) < count {
		return nil, fmt.Errorf("found %d of %d cards: %w", len(cards), count, cabinetcopy_service.ErrNotEnoughCards)
	}
	prices, err := reader.readPrices(ctx, cabinetID, cards)
	if err != nil {
		return nil, err
	}
	items := make([]cabinetcopy_service.PreparedItem, len(cards))
	seenVendors := make(map[string]struct{}, len(cards))
	for index, card := range cards {
		vendorCode := strings.TrimSpace(card.VendorCode)
		if vendorCode == "" {
			return nil, fmt.Errorf("WB card nmID=%d has empty vendor code", card.NMID)
		}
		if _, exists := seenVendors[vendorCode]; exists {
			return nil, fmt.Errorf("WB card vendor code %q is duplicated", vendorCode)
		}
		seenVendors[vendorCode] = struct{}{}
		mapped, mapErr := mapCard(card, prices[card.NMID], index+1)
		if mapErr != nil {
			return nil, mapErr
		}
		items[index] = cabinetcopy_service.PreparedItem{
			Position: index + 1,
			NMID:     card.NMID,
			IMTID:    card.IMTID,
			Card:     mapped,
		}
	}
	return items, nil
}

func (reader *Reader) readContentCards(ctx context.Context, cabinetID cabinetcopy_service.CabinetID, tagIDs []int64, count int) ([]contentapi.Card, error) {
	result := make([]contentapi.Card, 0, count)
	request := contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
		Sort: contentapi.CardsSort{Ascending: true}, Cursor: contentapi.CardsListCursor{Limit: min(count, contentapi.MaxCardsListPageSize)},
	}}
	if len(tagIDs) > 0 {
		request.Settings.Filter = &contentapi.CardsListFilter{TagIDs: append([]int64(nil), tagIDs...)}
	}
	_, err := wb_catalog.WalkCards(ctx, reader.content, cabinetID, contentapi.CardsListQuery{}, request, count+1, func(card contentapi.Card) bool {
		result = append(result, card)
		return len(result) < count
	})
	if err != nil {
		return nil, fmt.Errorf("list source WB cards: %w", err)
	}
	return result, nil
}

func (reader *Reader) readPrices(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
	cards []contentapi.Card,
) (map[int64]map[int64]int64, error) {
	result := make(map[int64]map[int64]int64, len(cards))
	for start := 0; start < len(cards); start += maxPriceBatchSize {
		end := start + maxPriceBatchSize
		if end > len(cards) {
			end = len(cards)
		}
		nmList := make([]int64, end-start)
		for index := start; index < end; index++ {
			nmList[index-start] = cards[index].NMID
		}
		response, executeErr := reader.prices.GoodsByNM(
			ctx, cabinetID,
			pricesapi.GoodsByNMRequest{NMList: nmList},
		)
		if executeErr != nil {
			return nil, fmt.Errorf("get prices for source WB cards: %w", executeErr)
		}
		if response.Error {
			return nil, fmt.Errorf("WB prices response: %s", strings.TrimSpace(response.ErrorText))
		}
		for _, good := range response.Data.ListGoods {
			if good.NMID <= 0 {
				return nil, errors.New("WB prices response contains invalid nmID")
			}
			if _, exists := result[good.NMID]; exists {
				return nil, fmt.Errorf("WB prices response duplicates nmID=%d", good.NMID)
			}
			sizes := make(map[int64]int64, len(good.Sizes))
			for _, size := range good.Sizes {
				if size.SizeID <= 0 || size.Price <= 0 {
					return nil, fmt.Errorf("WB price for nmID=%d contains invalid size", good.NMID)
				}
				sizes[size.SizeID] = size.Price
			}
			result[good.NMID] = sizes
		}
	}
	for _, card := range cards {
		if len(result[card.NMID]) == 0 {
			return nil, fmt.Errorf("WB price is unavailable for nmID=%d", card.NMID)
		}
	}
	return result, nil
}

func mapCard(
	card contentapi.Card,
	prices map[int64]int64,
	position int,
) (cardimport_service.AggregatedCard, error) {
	if card.SubjectID <= 0 || strings.TrimSpace(card.SubjectName) == "" || len(card.Sizes) == 0 {
		return cardimport_service.AggregatedCard{}, fmt.Errorf("WB card nmID=%d has incomplete content", card.NMID)
	}
	sizes := make([]cardimport_service.ParsedSize, len(card.Sizes))
	var firstPrice int64
	for index, size := range card.Sizes {
		price := prices[size.CHRTID]
		if price <= 0 {
			return cardimport_service.AggregatedCard{}, fmt.Errorf("WB price is unavailable for nmID=%d chrtID=%d", card.NMID, size.CHRTID)
		}
		if firstPrice == 0 {
			firstPrice = price
		}
		sizes[index] = cardimport_service.ParsedSize{
			TechSize: strings.TrimSpace(size.TechSize),
			WBSize:   strings.TrimSpace(size.WBSize),
			Price:    price,
			SKUs:     []string{},
		}
	}
	characteristics := make([]cardimport_service.RawCharacteristic, 0, len(card.Characteristics))
	for _, characteristic := range card.Characteristics {
		if characteristic.ID <= 0 || strings.TrimSpace(characteristic.Name) == "" || characteristic.Value == nil {
			return cardimport_service.AggregatedCard{}, fmt.Errorf("WB card nmID=%d has invalid characteristic", card.NMID)
		}
		characteristics = append(characteristics, cardimport_service.RawCharacteristic{
			ID:    characteristic.ID,
			Name:  strings.TrimSpace(characteristic.Name),
			Value: characteristic.Value,
		})
	}
	photos := make([]string, 0, len(card.Photos))
	for _, photo := range card.Photos {
		link := firstNonEmpty(photo.Big, photo.C516x688, photo.C246x328, photo.Square, photo.TM)
		if link != "" {
			photos = append(photos, link)
		}
	}
	group := ""
	if card.IMTID > 0 {
		group = fmt.Sprintf("wb-imt:%d", card.IMTID)
	}
	marked := "нет"
	if card.KIZMarked {
		marked = "да"
	}
	kizRequired := "нет"
	if card.NeedKIZ {
		kizRequired = "да"
	}
	return cardimport_service.AggregatedCard{
		SourceRows:       []int{position},
		Group:            group,
		Category:         strings.TrimSpace(card.SubjectName),
		Price:            firstPrice,
		KIZRequirement:   kizRequired,
		MarkingConfirmed: marked,
		Variant: cardimport_service.ParsedVariant{
			VendorCode:  strings.TrimSpace(card.VendorCode),
			Title:       strings.TrimSpace(card.Title),
			Description: strings.TrimSpace(card.Description),
			Brand:       strings.TrimSpace(card.Brand),
			Dimensions: cardimport_service.ParsedDimensions{
				Length:       card.Dimensions.Length,
				Width:        card.Dimensions.Width,
				Height:       card.Dimensions.Height,
				WeightBrutto: card.Dimensions.WeightBrutto,
			},
			Sizes:           sizes,
			Characteristics: characteristics,
		},
		Media: cardimport_service.ParsedMedia{
			Photos: photos,
			Video:  strings.TrimSpace(card.Video),
		},
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

var _ cabinetcopy_service.Reader = (*Reader)(nil)
