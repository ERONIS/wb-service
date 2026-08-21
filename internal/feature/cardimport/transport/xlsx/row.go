package cardimport_xlsx_transport

import (
	"fmt"
	"math"
	"strings"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const weightComparisonTolerance = 0.01

func parseRow(
	sheetName string,
	rowNumber int,
	row []string,
	columns columns,
) (cardimport_service.ParsedRow, []cardimport_service.ParseIssue) {
	issues := make([]cardimport_service.ParseIssue, 0)

	group := strings.TrimSpace(columns.value(row, columnGroup))
	vendorCode := requiredText(
		columns.value(row, columnVendorCode),
		"vendor_code_required",
		"Не заполнен артикул продавца.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnVendorCode),
		&issues,
	)
	title := requiredText(
		columns.value(row, columnTitle),
		"title_required",
		"Не заполнено наименование товара.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnTitle),
		&issues,
	)
	category := requiredText(
		columns.value(row, columnCategory),
		"category_required",
		"Не заполнена категория продавца.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnCategory),
		&issues,
	)
	brand := requiredText(
		columns.value(row, columnBrand),
		"brand_required",
		"Не заполнен бренд.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnBrand),
		&issues,
	)
	description := requiredText(
		columns.value(row, columnDescription),
		"description_required",
		"Не заполнено описание товара.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnDescription),
		&issues,
	)

	price := parsePositiveIntField(
		columns.value(row, columnPrice),
		"price_invalid",
		"Цена должна быть положительным целым числом.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnPrice),
		&issues,
	)
	weight := parsePositiveFloatField(
		columns.value(row, columnWeightBrutto),
		"weight_invalid",
		"Вес с упаковкой должен быть положительным числом в килограммах.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnWeightBrutto),
		&issues,
	)
	height := parsePositiveFloatField(
		columns.value(row, columnHeight),
		"height_invalid",
		"Высота упаковки должна быть положительным числом.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnHeight),
		&issues,
	)
	length := parsePositiveFloatField(
		columns.value(row, columnLength),
		"length_invalid",
		"Длина упаковки должна быть положительным числом.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnLength),
		&issues,
	)
	width := parsePositiveFloatField(
		columns.value(row, columnWidth),
		"width_invalid",
		"Ширина упаковки должна быть положительным числом.",
		sheetName,
		rowNumber,
		columns.firstColumn(columnWidth),
		&issues,
	)

	barcodes := splitValues(columns.value(row, columnSKUs), true)

	validateWeightGrams(
		columns.value(row, columnWeightGrams),
		weight,
		sheetName,
		rowNumber,
		columns.firstColumn(columnWeightGrams),
		&issues,
	)

	videos := splitValues(columns.value(row, columnVideo), false)
	video := ""
	if len(videos) > 0 {
		video = videos[0]
	}
	if len(videos) > 1 {
		issues = append(issues, newIssue(
			"too_many_videos",
			"Допускается только одна ссылка на видео.",
			sheetName,
			rowNumber,
			columns.firstColumn(columnVideo),
		))
	}

	parsed := cardimport_service.ParsedRow{
		SourceRow:        rowNumber,
		Group:            group,
		Category:         category,
		Price:            price,
		KIZRequirement:   columns.value(row, columnKIZ),
		AdultOnly:        columns.value(row, columnAdultOnly),
		MarkingConfirmed: columns.value(row, columnMarkingConfirmed),
		VATRate:          columns.value(row, columnVATRate),
		Variant: cardimport_service.ParsedVariant{
			VendorCode:  vendorCode,
			Title:       title,
			Description: description,
			Brand:       brand,
			Dimensions: cardimport_service.ParsedDimensions{
				Length:       length,
				Width:        width,
				Height:       height,
				WeightBrutto: weight,
			},
			Sizes: []cardimport_service.ParsedSize{{
				TechSize: columns.value(row, columnTechSize),
				WBSize:   columns.value(row, columnWBSize),
				SKUs:     barcodes,
			}},
			Characteristics: columns.characteristics(row),
		},
		Media: cardimport_service.ParsedMedia{
			Photos: splitValues(columns.value(row, columnPhoto), false),
			Video:  video,
		},
	}

	return parsed, issues
}

func requiredText(
	value string,
	code string,
	message string,
	sheetName string,
	row int,
	column string,
	issues *[]cardimport_service.ParseIssue,
) string {
	value = strings.TrimSpace(value)
	if value == "" {
		*issues = append(
			*issues,
			newIssue(code, message, sheetName, row, column),
		)
	}

	return value
}

func parsePositiveIntField(
	value string,
	code string,
	message string,
	sheetName string,
	row int,
	column string,
	issues *[]cardimport_service.ParseIssue,
) int64 {
	number, err := parsePositiveInt64(value)
	if err != nil {
		*issues = append(
			*issues,
			newIssue(
				code,
				fmt.Sprintf("%s Получено: %q.", message, value),
				sheetName,
				row,
				column,
			),
		)
	}

	return number
}

func parsePositiveFloatField(
	value string,
	code string,
	message string,
	sheetName string,
	row int,
	column string,
	issues *[]cardimport_service.ParseIssue,
) float64 {
	number, err := parsePositiveFloat64(value)
	if err != nil {
		*issues = append(
			*issues,
			newIssue(
				code,
				fmt.Sprintf("%s Получено: %q.", message, value),
				sheetName,
				row,
				column,
			),
		)
	}

	return number
}

func validateWeightGrams(
	value string,
	weightKilograms float64,
	sheetName string,
	row int,
	column string,
	issues *[]cardimport_service.ParseIssue,
) {
	if strings.TrimSpace(value) == "" || column == "" {
		return
	}

	weightGrams, err := parsePositiveFloat64(value)
	if err != nil {
		*issues = append(*issues, newIssue(
			"weight_grams_invalid",
			fmt.Sprintf(
				"Вес с упаковкой в граммах должен быть положительным числом. Получено: %q.",
				value,
			),
			sheetName,
			row,
			column,
		))
		return
	}
	if weightKilograms <= 0 {
		return
	}
	if math.Abs(weightKilograms*1000-weightGrams) > weightComparisonTolerance {
		*issues = append(*issues, newIssue(
			"weight_units_conflict",
			"Вес с упаковкой в килограммах и граммах не совпадает.",
			sheetName,
			row,
			column,
		))
	}
}

func newIssue(
	code string,
	message string,
	sheetName string,
	row int,
	column string,
) cardimport_service.ParseIssue {
	return cardimport_service.ParseIssue{
		Severity:  cardimport_service.IssueSeverityError,
		Code:      code,
		Message:   message,
		SheetName: sheetName,
		Row:       row,
		Column:    column,
	}
}

func hasErrors(issues []cardimport_service.ParseIssue) bool {
	for _, issue := range issues {
		if issue.Severity == cardimport_service.IssueSeverityError {
			return true
		}
	}

	return false
}
