package cardimport_xlsx_transport

import (
	"fmt"
	"strings"
	"unicode"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

const (
	columnGroup            = "group"
	columnVendorCode       = "vendor_code"
	columnWBArticleIgnored = "wb_article_ignored"
	columnTitle            = "title"
	columnCategory         = "category"
	columnBrand            = "brand"
	columnDescription      = "description"
	columnPhoto            = "photo"
	columnVideo            = "video"
	columnKIZ              = "kiz"
	columnWeightBrutto     = "weight_brutto"
	columnAdultOnly        = "adult_only"
	columnMarkingConfirmed = "marking_confirmed"
	columnSKUs             = "skus"
	columnTechSize         = "tech_size"
	columnWBSize           = "wb_size"
	columnPrice            = "price"
	columnVATRate          = "vat_rate"
	columnWeightGrams      = "weight_grams"
	columnHeight           = "height"
	columnLength           = "length"
	columnWidth            = "width"
)

var headerAliases = map[string]string{
	"group":              columnGroup,
	"группа":             columnGroup,
	"vendorcode":         columnVendorCode,
	"артикулпродавца":    columnVendorCode,
	"artikulwb":          columnWBArticleIgnored,
	"артикулwb":          columnWBArticleIgnored,
	"title":              columnTitle,
	"наименование":       columnTitle,
	"category":           columnCategory,
	"категорияпродавца":  columnCategory,
	"brand":              columnBrand,
	"бренд":              columnBrand,
	"description":        columnDescription,
	"описание":           columnDescription,
	"photo":              columnPhoto,
	"фото":               columnPhoto,
	"video":              columnVideo,
	"видео":              columnVideo,
	"kiz":                columnKIZ,
	"киз":                columnKIZ,
	"weightbrutto":       columnWeightBrutto,
	"вессупаковкой(кг)":  columnWeightBrutto,
	"вессупаковкойкг":    columnWeightBrutto,
	"вессупаковкой(кг.)": columnWeightBrutto,
	"18+":                columnAdultOnly,
	"подтверждаю,чтотоварпромаркирован": columnMarkingConfirmed,
	"skus":       columnSKUs,
	"баркоды":    columnSKUs,
	"бракоды":    columnSKUs,
	"techsize":   columnTechSize,
	"размер":     columnTechSize,
	"wbsize":     columnWBSize,
	"рос.размер": columnWBSize,
	"price":      columnPrice,
	"цена":       columnPrice,
	"ставкандс":  columnVATRate,
	"вестоварасупаковкой(г)": columnWeightGrams,
	"height":         columnHeight,
	"высотаупаковки": columnHeight,
	"length":         columnLength,
	"длинаупаковки":  columnLength,
	"width":          columnWidth,
	"ширинаупаковки": columnWidth,
}

var requiredColumns = []string{
	columnGroup,
	columnVendorCode,
	columnTitle,
	columnCategory,
	columnBrand,
	columnDescription,
	columnWeightBrutto,
	columnPrice,
	columnHeight,
	columnLength,
	columnWidth,
}

type columns struct {
	headers       []string
	indexesByName map[string][]int
	systemIndexes map[int]struct{}
}

func newColumns(headerRow []string) (columns, error) {
	result := columns{
		headers:       append([]string(nil), headerRow...),
		indexesByName: make(map[string][]int),
		systemIndexes: make(map[int]struct{}),
	}

	for index, header := range headerRow {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}

		canonicalName, known := headerAliases[normalizeHeader(header)]
		if !known {
			continue
		}

		result.indexesByName[canonicalName] = append(
			result.indexesByName[canonicalName],
			index,
		)
		result.systemIndexes[index] = struct{}{}
	}

	missing := make([]string, 0)
	for _, name := range requiredColumns {
		if len(result.indexesByName[name]) == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return columns{}, fmt.Errorf(
			"required XLSX columns are missing: %s",
			strings.Join(missing, ", "),
		)
	}

	return result, nil
}

func (c columns) value(row []string, name string) string {
	for _, index := range c.indexesByName[name] {
		if index < 0 || index >= len(row) {
			continue
		}

		if value := strings.TrimSpace(row[index]); value != "" {
			return value
		}
	}

	return ""
}

func (c columns) firstColumn(name string) string {
	indexes := c.indexesByName[name]
	if len(indexes) == 0 {
		return ""
	}

	columnName, _ := columnName(indexes[0])

	return columnName
}

func (c columns) characteristics(
	row []string,
) []cardimport_service.RawCharacteristic {
	characteristics := make(
		[]cardimport_service.RawCharacteristic,
		0,
	)

	for index, rawValue := range row {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		if _, system := c.systemIndexes[index]; system {
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
			cardimport_service.RawCharacteristic{
				Name:  header,
				Value: value,
			},
		)
	}

	return characteristics
}

func isHeaderRow(row []string) bool {
	foundVendor := false
	foundCategory := false

	for _, cell := range row {
		switch headerAliases[normalizeHeader(cell)] {
		case columnVendorCode:
			foundVendor = true
		case columnCategory:
			foundCategory = true
		}
	}

	return foundVendor && foundCategory
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
