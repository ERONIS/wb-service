package cardimport_service

import (
	"fmt"
	"reflect"
	"strings"
)

type barcodeSource struct {
	row        int
	vendorCode string
}

// AggregateParsedFile объединяет размеры одной карточки только внутри файла.
// Межфайловые конфликты проверяются репозиторием по durable результатам всей
// сессии.
func AggregateParsedFile(parsed ParsedFile) AggregatedFile {
	result := AggregatedFile{
		FileID:    parsed.FileID,
		SheetName: parsed.SheetName,
		Cards:     make([]AggregatedCard, 0, len(parsed.Rows)),
		Issues:    append([]ParseIssue(nil), parsed.Issues...),
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
			result.Issues = append(result.Issues, ParseIssue{
				Severity: IssueSeverityError,
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

func cardFromRow(row ParsedRow) AggregatedCard {
	return AggregatedCard{
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

func cloneVariant(variant ParsedVariant) ParsedVariant {
	variant.Sizes = appendSizes(nil, variant.Sizes)
	variant.Characteristics = append(
		[]RawCharacteristic(nil),
		variant.Characteristics...,
	)

	return variant
}

func cloneMedia(media ParsedMedia) ParsedMedia {
	media.Photos = append([]string(nil), media.Photos...)

	return media
}

func appendSizes(target []ParsedSize, sizes []ParsedSize) []ParsedSize {
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

func duplicateBarcodeIssues(
	sheetName string,
	row ParsedRow,
	seen map[string]barcodeSource,
) []ParseIssue {
	issues := make([]ParseIssue, 0)
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

			issues = append(issues, ParseIssue{
				Severity: IssueSeverityError,
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

func conflictingCardFields(card AggregatedCard, row ParsedRow) []string {
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
