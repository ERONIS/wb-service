package cardimport_intake

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

type barcodeSource struct {
	row        int
	vendorCode string
}

// AggregateParsedFile объединяет строки одного файла в карточки и собирает ошибки.
func AggregateParsedFile(parsed cardimport_model.ParsedFile) cardimport_model.AggregatedFile {
	result := cardimport_model.AggregatedFile{
		FileID:    parsed.FileID,
		SheetName: parsed.SheetName,
		Cards:     make([]cardpipeline.Card, 0, len(parsed.Rows)),
		Issues:    append([]cardimport_model.ParseIssue(nil), parsed.Issues...),
	}

	cardIndexes := make(map[string]int, len(parsed.Rows))
	barcodes := make(map[string]barcodeSource)

	for _, row := range parsed.Rows {
		result.Issues = append(
			result.Issues,
			duplicateBarcodeIssues(parsed.SheetName, row, barcodes)...,
		)

		vendorCode := strings.TrimSpace(row.Variant.VendorCode)
		cardIndex, exists := cardIndexes[vendorCode]
		if !exists {
			cardIndexes[vendorCode] = len(result.Cards)
			result.Cards = append(result.Cards, cardFromRow(row))
			continue
		}

		card := &result.Cards[cardIndex]
		if fields := conflictingCardFields(*card, row); len(fields) > 0 {
			result.Issues = append(result.Issues, cardimport_model.ParseIssue{
				Severity: cardimport_model.IssueSeverityError,
				Code:     "vendor_code_fields_conflict",
				Message: fmt.Sprintf(
					"Строка с артикулом продавца %q конфликтует с предыдущей: различаются поля %s.",
					vendorCode,
					strings.Join(fields, ", "),
				),
				SheetName: parsed.SheetName,
				Row:       row.SourceRow,
				Column:    "Артикул продавца",
			})
			continue
		}

		card.SourceRows = append(card.SourceRows, row.SourceRow)
		card.Variant.Sizes = appendSizes(card.Variant.Sizes, row.Variant.Sizes)
	}

	return result
}

// duplicateBarcodeIssues находит повторные баркоды в строках одного файла.
func duplicateBarcodeIssues(
	sheetName string,
	row cardimport_model.ParsedRow,
	seen map[string]barcodeSource,
) []cardimport_model.ParseIssue {
	issues := make([]cardimport_model.ParseIssue, 0)
	vendorCode := strings.TrimSpace(row.Variant.VendorCode)

	for _, size := range row.Variant.Sizes {
		for _, barcode := range size.SKUs {
			barcode = strings.TrimSpace(barcode)
			if barcode == "" {
				continue
			}

			first, exists := seen[barcode]
			if !exists {
				seen[barcode] = barcodeSource{
					row:        row.SourceRow,
					vendorCode: vendorCode,
				}
				continue
			}

			issues = append(issues, cardimport_model.ParseIssue{
				Severity: cardimport_model.IssueSeverityError,
				Code:     "duplicate_barcode_in_file",
				Message: fmt.Sprintf(
					"Баркод %q уже используется в строке %d у артикула продавца %q.",
					barcode,
					first.row,
					first.vendorCode,
				),
				SheetName: sheetName,
				Row:       row.SourceRow,
				Column:    "Баркоды",
			})
		}
	}

	return issues
}

// conflictingCardFields находит различия между строками одной карточки.
func conflictingCardFields(card cardpipeline.Card, row cardimport_model.ParsedRow) []string {
	fields := make([]string, 0)
	appendConflict := func(different bool, name string) {
		if different {
			fields = append(fields, name)
		}
	}

	appendConflict(card.Group != row.Group, "группа")
	appendConflict(card.Category != row.Category, "категория")
	appendConflict(card.Price != row.Price, "цена")
	appendConflict(card.KIZRequirement != row.KIZRequirement, "КИЗ")
	appendConflict(card.AdultOnly != row.AdultOnly, "18+")
	appendConflict(
		card.MarkingConfirmed != row.MarkingConfirmed,
		"маркировка",
	)
	appendConflict(card.VATRate != row.VATRate, "НДС")
	appendConflict(card.Variant.Title != row.Variant.Title, "наименование")
	appendConflict(
		card.Variant.Description != row.Variant.Description,
		"описание",
	)
	appendConflict(card.Variant.Brand != row.Variant.Brand, "бренд")
	appendConflict(
		!reflect.DeepEqual(card.Variant.Dimensions, row.Variant.Dimensions),
		"габариты",
	)
	appendConflict(
		!reflect.DeepEqual(
			card.Variant.Characteristics,
			row.Variant.Characteristics,
		),
		"характеристики",
	)
	appendConflict(!reflect.DeepEqual(card.Media, row.Media), "медиа")

	return fields
}

// cardFromRow создаёт карточку из первой строки товара.
func cardFromRow(row cardimport_model.ParsedRow) cardpipeline.Card {
	return cardpipeline.Card{
		SourceRows:       []int{row.SourceRow},
		Group:            row.Group,
		Category:         row.Category,
		Price:            row.Price,
		KIZRequirement:   row.KIZRequirement,
		AdultOnly:        row.AdultOnly,
		MarkingConfirmed: row.MarkingConfirmed,
		VATRate:          row.VATRate,
		Variant:          cloneVariant(row.Variant),
		Media:            cloneMedia(row.Media),
	}
}

// cloneVariant копирует вариант карточки вместе со срезами.
func cloneVariant(variant cardpipeline.Variant) cardpipeline.Variant {
	variant.Sizes = appendSizes(nil, variant.Sizes)
	variant.Characteristics = append(
		[]cardpipeline.RawCharacteristic(nil),
		variant.Characteristics...,
	)

	return variant
}

// appendSizes добавляет размеры и уникальные баркоды к варианту.
func appendSizes(target []cardpipeline.Size, sizes []cardpipeline.Size) []cardpipeline.Size {
	for _, size := range sizes {
		targetIndex := -1
		for index := range target {
			if target[index].TechSize == size.TechSize &&
				target[index].WBSize == size.WBSize {
				targetIndex = index
				break
			}
		}

		if targetIndex < 0 {
			cloned := size
			cloned.SKUs = append([]string(nil), size.SKUs...)
			target = append(target, cloned)
			continue
		}

		knownSKUs := make(map[string]struct{}, len(target[targetIndex].SKUs))
		for _, sku := range target[targetIndex].SKUs {
			knownSKUs[sku] = struct{}{}
		}
		for _, sku := range size.SKUs {
			if _, exists := knownSKUs[sku]; exists {
				continue
			}
			target[targetIndex].SKUs = append(target[targetIndex].SKUs, sku)
			knownSKUs[sku] = struct{}{}
		}
	}

	return target
}

// cloneMedia копирует список фотографий карточки.
func cloneMedia(media cardpipeline.Media) cardpipeline.Media {
	media.Photos = append([]string(nil), media.Photos...)

	return media
}
