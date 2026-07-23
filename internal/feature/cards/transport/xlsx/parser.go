package cards_xlsx_transport

import (
	"fmt"
	"io"

	"github.com/ERONIS/wb-service/internal/core/domain"
)

// ParseCards parses cards from the first worksheet of an XLSX document.
func ParseCards(reader io.Reader) ([]domain.Card, error) {
	rows, err := readFirstSheetRows(reader)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	headerIndex := findHeaderRow(rows, columnVendorCode)
	if headerIndex == -1 {
		return nil, fmt.Errorf(
			"не нашли строку с заголовками (колонка 'Артикул продавца')",
		)
	}

	columns := newCardColumns(rows[headerIndex])
	cards := make([]domain.Card, 0)
	cardIndexByVendor := make(map[string]int)

	for _, row := range rows[headerIndex+1:] {
		if isRowEmpty(row) {
			continue
		}

		card, ok := parseCardRow(row, columns)
		if !ok {
			continue
		}

		vendorCode := card.Variant.VendorCode
		if index, exists := cardIndexByVendor[vendorCode]; exists {
			mergeCard(&cards[index], card)
			continue
		}

		cardIndexByVendor[vendorCode] = len(cards)
		cards = append(cards, card)
	}

	return cards, nil
}
