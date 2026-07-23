package cards_xlsx_transport

import (
	"strings"
	"unicode"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

const (
	columnCategory         = "category"
	columnVendorCode       = "vendorcode"
	columnWBArticle        = "artikulwb"
	columnTitle            = "title"
	columnDescription      = "description"
	columnBrand            = "brand"
	columnWeightBrutto     = "weightbrutto"
	columnLength           = "length"
	columnWidth            = "width"
	columnHeight           = "height"
	columnTechSize         = "techsize"
	columnWBSize           = "wbsize"
	columnPrice            = "price"
	columnGroupID          = "groupid"
	columnSKUs             = "skus"
	columnWholesaleEnabled = "wholesaleenabled"
	columnWholesaleQuantum = "wholesalequantum"
	columnPhoto            = "photo"
	columnVideo            = "video"
)

// cardHeaders maps every supported normalized XLSX header to a domain field.
// Add new aliases here without changing parsing code.
var cardHeaders = map[string]string{
	columnCategory:         columnCategory,
	"категорияпродавца":    columnCategory,
	columnVendorCode:       columnVendorCode,
	"артикулпродавца":      columnVendorCode,
	columnWBArticle:        columnWBArticle,
	"артикулwb":            columnWBArticle,
	columnTitle:            columnTitle,
	"наименование":         columnTitle,
	columnDescription:      columnDescription,
	"описание":             columnDescription,
	columnBrand:            columnBrand,
	"бренд":                columnBrand,
	columnSKUs:             columnSKUs,
	"баркоды":              columnSKUs,
	"бракоды":              columnSKUs,
	columnPhoto:            columnPhoto,
	"фото":                 columnPhoto,
	columnVideo:            columnVideo,
	"видео":                columnVideo,
	columnPrice:            columnPrice,
	"цена":                 columnPrice,
	columnGroupID:          columnGroupID,
	"группа":               columnGroupID,
	columnWeightBrutto:     columnWeightBrutto,
	"вессупаковкой(кг)":    columnWeightBrutto,
	"вессупаковкойкг":      columnWeightBrutto,
	"вессупаковкой(кг.)":   columnWeightBrutto,
	columnWidth:            columnWidth,
	"ширинаупаковки":       columnWidth,
	columnHeight:           columnHeight,
	"высотаупаковки":       columnHeight,
	columnLength:           columnLength,
	"длинаупаковки":        columnLength,
	columnTechSize:         columnTechSize,
	columnWBSize:           columnWBSize,
	columnWholesaleEnabled: columnWholesaleEnabled,
	columnWholesaleQuantum: columnWholesaleQuantum,
}

type cardColumns struct {
	headers       []string
	indexesByName map[string][]int
	knownIndexes  map[int]struct{}
}

func newCardColumns(headerRow []string) cardColumns {
	columns := cardColumns{
		headers:       headerRow,
		indexesByName: make(map[string][]int),
		knownIndexes:  make(map[int]struct{}),
	}

	for index, header := range headerRow {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}

		normalized := normalizeHeader(header)
		if normalized == "" {
			continue
		}

		canonicalName, known := cardHeaders[normalized]
		if !known {
			continue
		}

		columns.indexesByName[canonicalName] = append(
			columns.indexesByName[canonicalName],
			index,
		)
		columns.knownIndexes[index] = struct{}{}
	}

	return columns
}

func (c cardColumns) value(row []string, name string) string {
	for _, index := range c.indexesByName[name] {
		if index < 0 || index >= len(row) {
			continue
		}

		value := strings.TrimSpace(row[index])
		if value != "" {
			return value
		}
	}

	return ""
}

func (c cardColumns) rawCharacteristics(
	row []string,
) []domain.RawCharacteristic {
	characteristics := make([]domain.RawCharacteristic, 0)

	for index, value := range row {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, known := c.knownIndexes[index]; known {
			continue
		}

		if index >= len(c.headers) {
			continue
		}
		header := strings.TrimSpace(c.headers[index])
		if header == "" {
			continue
		}

		characteristics = append(
			characteristics,
			domain.RawCharacteristic{
				Name:  header,
				Value: value,
			},
		)
	}

	return characteristics
}

func findHeaderRow(rows [][]string, requiredColumn string) int {
	for rowIndex, row := range rows {
		for _, cell := range row {
			canonicalName := cardHeaders[normalizeHeader(cell)]
			if canonicalName == requiredColumn {
				return rowIndex
			}
		}
	}

	return -1
}

func normalizeHeader(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))

	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return character
	}, value)
}
