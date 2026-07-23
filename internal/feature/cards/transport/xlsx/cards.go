package cards_xlsx_transport

import (
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

func parseCardRow(row []string, columns cardColumns) (domain.Card, bool) {
	vendorCode := columns.value(row, columnVendorCode)
	if vendorCode == "" || strings.HasPrefix(vendorCode, "Это ") {
		return domain.Card{}, false
	}

	card := domain.Card{
		Category: columns.value(row, columnCategory),
		GroupID:  parseGroupID(columns.value(row, columnGroupID)),
		Price:    parseInt(columns.value(row, columnPrice)),
		Variant: domain.Variant{
			VendorCode:  vendorCode,
			Title:       columns.value(row, columnTitle),
			Description: columns.value(row, columnDescription),
			Brand:       columns.value(row, columnBrand),
			Wholesale: domain.Wholesale{
				Enabled: parseBool(columns.value(row, columnWholesaleEnabled)),
				Quantum: parseInt(columns.value(row, columnWholesaleQuantum)),
			},
			Dimensions: domain.Dimensions{
				Length:       parseFloat(columns.value(row, columnLength)),
				Width:        parseFloat(columns.value(row, columnWidth)),
				Height:       parseFloat(columns.value(row, columnHeight)),
				WeightBrutto: parseFloat(columns.value(row, columnWeightBrutto)),
			},
			RawCharacteristics: columns.rawCharacteristics(row),
		},
	}

	techSize := columns.value(row, columnTechSize)
	wbSize := columns.value(row, columnWBSize)
	skus := splitList(columns.value(row, columnSKUs))
	if techSize != "" || wbSize != "" || len(skus) > 0 {
		card.Variant.Sizes = []domain.Size{{
			TechSize: techSize,
			WbSize:   wbSize,
			Skus:     skus,
		}}
	}

	photos := splitList(columns.value(row, columnPhoto))
	videos := splitList(columns.value(row, columnVideo))
	if len(photos) > 0 || len(videos) > 0 {
		card.Media = &domain.Media{Photos: photos, Videos: videos}
	}

	return card, true
}

func mergeCard(target *domain.Card, source domain.Card) {
	if target.Category == "" {
		target.Category = source.Category
	}
	if target.GroupID == 0 {
		target.GroupID = source.GroupID
	}
	if target.Price == 0 {
		target.Price = source.Price
	}
	if target.Variant.Title == "" {
		target.Variant.Title = source.Variant.Title
	}
	if target.Variant.Description == "" {
		target.Variant.Description = source.Variant.Description
	}
	if target.Variant.Brand == "" {
		target.Variant.Brand = source.Variant.Brand
	}

	target.Variant.Sizes = append(target.Variant.Sizes, source.Variant.Sizes...)
	target.Variant.RawCharacteristics = append(
		target.Variant.RawCharacteristics,
		source.Variant.RawCharacteristics...,
	)

	if source.Media == nil {
		return
	}
	if target.Media == nil {
		target.Media = &domain.Media{}
	}
	target.Media.Photos = append(target.Media.Photos, source.Media.Photos...)
	target.Media.Videos = append(target.Media.Videos, source.Media.Videos...)
}
