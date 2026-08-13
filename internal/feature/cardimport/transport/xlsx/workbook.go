package cardimport_xlsx_transport

import (
	"fmt"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	productsSheetName = "Товары"
	maxUnpackedSize   = 128 << 20
	maxWorksheetXML   = 32 << 20
	maxRows           = 100_000
	maxCells          = 2_000_000
)

func openWorkbook(reader io.Reader) (*excelize.File, error) {
	if reader == nil {
		return nil, fmt.Errorf("XLSX reader is nil")
	}

	workbook, err := excelize.OpenReader(
		reader,
		excelize.Options{
			UnzipSizeLimit:    maxUnpackedSize,
			UnzipXMLSizeLimit: maxWorksheetXML,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("open XLSX workbook: %w", err)
	}

	return workbook, nil
}

func findProductsSheet(workbook *excelize.File) (string, error) {
	for _, sheetName := range workbook.GetSheetList() {
		if strings.EqualFold(
			strings.TrimSpace(sheetName),
			productsSheetName,
		) {
			return sheetName, nil
		}
	}

	return "", fmt.Errorf("XLSX sheet %q was not found", productsSheetName)
}

func columnName(index int) (string, error) {
	name, err := excelize.ColumnNumberToName(index + 1)
	if err != nil {
		return "", fmt.Errorf("resolve XLSX column %d: %w", index+1, err)
	}

	return name, nil
}
