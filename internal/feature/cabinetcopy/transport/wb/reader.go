package cabinetcopy_wb_transport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	pricesapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/prices/v2"
	client "github.com/ERONIS/wb-service/internal/core/transport/wb/client"
	wbconfig "github.com/ERONIS/wb-service/internal/core/transport/wb/config"
	cabinetcopy_service "github.com/ERONIS/wb-service/internal/feature/cabinetcopy/service"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

const maxPriceBatchSize = 1000

type Clientset interface {
	Cabinets() []core_wb.CabinetInfo
	ExecutorForCabinet(wbconfig.CabinetID) (client.Executor, error)
	PricesExecutorForCabinet(wbconfig.CabinetID) (client.Executor, error)
}

type CabinetSource interface {
	Targets(context.Context, int64) ([]wbcabinet_service.TargetCredential, error)
}

type Reader struct {
	clientset Clientset
	cabinets  CabinetSource
}

func NewReader(clientset Clientset, cabinetSources ...CabinetSource) *Reader {
	if clientset == nil {
		panic("cabinet copy WB clientset is nil")
	}
	if len(cabinetSources) > 1 || (len(cabinetSources) == 1 && cabinetSources[0] == nil) {
		panic("cabinet copy WB cabinet source is invalid")
	}
	reader := &Reader{clientset: clientset}
	if len(cabinetSources) == 1 {
		reader.cabinets = cabinetSources[0]
	}
	return reader
}

func (reader *Reader) CabinetsForOwner(
	ctx context.Context,
	ownerTelegramID int64,
) ([]cabinetcopy_service.Cabinet, error) {
	if reader.cabinets == nil {
		return reader.Cabinets(), nil
	}
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

func (reader *Reader) Cabinets() []cabinetcopy_service.Cabinet {
	cabinets := reader.clientset.Cabinets()
	result := make([]cabinetcopy_service.Cabinet, len(cabinets))
	for index := range cabinets {
		result[index] = cabinetcopy_service.Cabinet{
			ID:   cabinetcopy_service.CabinetID(cabinets[index].ID),
			Name: cabinets[index].Name,
		}
	}
	return result
}

func (reader *Reader) ListTags(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
) ([]cabinetcopy_service.Tag, error) {
	executor, err := reader.contentExecutor(cabinetID)
	if err != nil {
		return nil, err
	}
	response, err := client.ExecuteResponse[contentapi.TagsResponse](
		ctx, executor, contentapi.TagsOperation(), nil, nil,
	)
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

func (reader *Reader) readContentCards(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
	tagIDs []int64,
	count int,
) ([]contentapi.Card, error) {
	executor, err := reader.contentExecutor(cabinetID)
	if err != nil {
		return nil, err
	}
	cursor := contentapi.CardsListCursor{}
	result := make([]contentapi.Card, 0, count)
	seen := make(map[int64]struct{}, count)
	for len(result) < count {
		limit := count - len(result)
		if limit > contentapi.MaxCardsListPageSize {
			limit = contentapi.MaxCardsListPageSize
		}
		cursor.Limit = limit
		var filter *contentapi.CardsListFilter
		if len(tagIDs) > 0 {
			filter = &contentapi.CardsListFilter{TagIDs: append([]int64(nil), tagIDs...)}
		}
		response, executeErr := client.ExecuteResponse[contentapi.CardsListResponse](
			ctx,
			executor,
			contentapi.CardsListOperation(),
			contentapi.CardsListQuery{},
			contentapi.CardsListRequest{Settings: contentapi.CardsListSettings{
				Sort:   contentapi.CardsSort{Ascending: true},
				Filter: filter,
				Cursor: cursor,
			}},
		)
		if executeErr != nil {
			return nil, fmt.Errorf("list source WB cards: %w", executeErr)
		}
		for _, card := range response.Cards {
			if card.NMID <= 0 {
				return nil, errors.New("WB cards response contains invalid nmID")
			}
			if _, exists := seen[card.NMID]; exists {
				continue
			}
			seen[card.NMID] = struct{}{}
			result = append(result, card)
			if len(result) == count {
				break
			}
		}
		if len(result) == count {
			break
		}
		if len(response.Cards) < limit {
			break
		}
		next := contentapi.CardsListCursor{
			UpdatedAt: response.Cursor.UpdatedAt,
			NMID:      response.Cursor.NMID,
		}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return nil, errors.New("WB cards cursor did not advance")
		}
		cursor.UpdatedAt = next.UpdatedAt
		cursor.NMID = next.NMID
	}
	return result, nil
}

func (reader *Reader) readPrices(
	ctx context.Context,
	cabinetID cabinetcopy_service.CabinetID,
	cards []contentapi.Card,
) (map[int64]map[int64]int64, error) {
	executor, err := reader.clientset.PricesExecutorForCabinet(wbconfig.CabinetID(cabinetID))
	if err != nil {
		return nil, fmt.Errorf("get WB Prices API executor: %w", err)
	}
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
		response, executeErr := client.ExecuteResponse[pricesapi.GoodsResponse](
			ctx,
			executor,
			pricesapi.GoodsByNMOperation(),
			nil,
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

func (reader *Reader) contentExecutor(cabinetID cabinetcopy_service.CabinetID) (client.Executor, error) {
	executor, err := reader.clientset.ExecutorForCabinet(wbconfig.CabinetID(cabinetID))
	if err != nil {
		return nil, fmt.Errorf("get WB Content API executor: %w", err)
	}
	return executor, nil
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
