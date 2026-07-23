package cards_xlsx_transport

import (
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"
)

func readFirstSheetRows(reader io.Reader) ([][]string, error) {
	workbook, err := excelize.OpenReader(reader)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = workbook.Close() }()

	sheetName := workbook.GetSheetName(0)
	if sheetName == "" {
		return nil, fmt.Errorf("empty workbook")
	}

	rows, err := workbook.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}

	return rows, nil
}
