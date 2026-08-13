package cardimport_xlsx_transport

import (
	"fmt"
	"io"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"github.com/xuri/excelize/v2"
)

const maxParsedRows = 50_000

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Parse(
	fileID cardimport_service.FileID,
	reader io.Reader,
) (cardimport_service.ParsedFile, error) {
	return Parse(fileID, reader)
}

// Parse разбирает строки листа "Товары". Каждая строка сохраняется отдельно;
// Артикул WB игнорируется, а Group имеет область переданного fileID.
func Parse(
	fileID cardimport_service.FileID,
	reader io.Reader,
) (cardimport_service.ParsedFile, error) {
	if fileID <= 0 {
		return cardimport_service.ParsedFile{}, fmt.Errorf(
			"cardimport file ID must be positive",
		)
	}

	workbook, err := openWorkbook(reader)
	if err != nil {
		return cardimport_service.ParsedFile{}, err
	}
	defer func() { _ = workbook.Close() }()

	sheetName, err := findProductsSheet(workbook)
	if err != nil {
		return cardimport_service.ParsedFile{}, err
	}

	rows, err := workbook.Rows(sheetName)
	if err != nil {
		return cardimport_service.ParsedFile{}, fmt.Errorf(
			"open rows from XLSX sheet %q: %w",
			sheetName,
			err,
		)
	}
	defer func() { _ = rows.Close() }()

	result := cardimport_service.ParsedFile{
		FileID:    fileID,
		SheetName: sheetName,
		Rows:      make([]cardimport_service.ParsedRow, 0),
		Issues:    make([]cardimport_service.ParseIssue, 0),
	}

	var parsedColumns columns
	headerFound := false
	totalCells := 0
	rowNumber := 0

	for rows.Next() {
		rowNumber++
		if rowNumber > maxRows {
			return cardimport_service.ParsedFile{}, fmt.Errorf(
				"XLSX row limit exceeded: %d",
				maxRows,
			)
		}

		row, rowErr := rows.Columns(
			excelize.Options{RawCellValue: true},
		)
		if rowErr != nil {
			return cardimport_service.ParsedFile{}, fmt.Errorf(
				"read XLSX sheet %q row %d: %w",
				sheetName,
				rowNumber,
				rowErr,
			)
		}

		totalCells += len(row)
		if totalCells > maxCells {
			return cardimport_service.ParsedFile{}, fmt.Errorf(
				"XLSX cell limit exceeded: %d",
				maxCells,
			)
		}

		if !headerFound {
			if !isHeaderRow(row) {
				continue
			}

			parsedColumns, err = newColumns(row)
			if err != nil {
				return cardimport_service.ParsedFile{}, fmt.Errorf(
					"read XLSX headers at row %d: %w",
					rowNumber,
					err,
				)
			}
			result.HeaderRow = rowNumber
			headerFound = true
			continue
		}

		// WB-шаблон содержит одну строку подсказок сразу после заголовков.
		if rowNumber == result.HeaderRow+1 || isRowEmpty(row) {
			continue
		}

		parsedRow, issues := parseRow(
			sheetName,
			rowNumber,
			row,
			parsedColumns,
		)
		result.Issues = append(result.Issues, issues...)
		if hasErrors(issues) {
			continue
		}

		result.Rows = append(result.Rows, parsedRow)
		if len(result.Rows) > maxParsedRows {
			return cardimport_service.ParsedFile{}, fmt.Errorf(
				"XLSX parsed row limit exceeded: %d",
				maxParsedRows,
			)
		}
	}

	if err := rows.Error(); err != nil {
		return cardimport_service.ParsedFile{}, fmt.Errorf(
			"iterate XLSX sheet %q: %w",
			sheetName,
			err,
		)
	}
	if !headerFound {
		return cardimport_service.ParsedFile{}, fmt.Errorf(
			"XLSX header row with seller article and category was not found",
		)
	}
	if len(result.Rows) == 0 && len(result.Issues) == 0 {
		result.Issues = append(result.Issues, newIssue(
			"products_empty",
			"В файле нет строк с товарами.",
			sheetName,
			result.HeaderRow,
			"",
		))
	}

	return result, nil
}
