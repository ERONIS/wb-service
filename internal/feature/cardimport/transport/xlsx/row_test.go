package cardimport_xlsx_transport

import (
	"testing"

	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

func TestParseRowRequiresGroup(t *testing.T) {
	row := make([]string, len(requiredColumns))
	for index, name := range requiredColumns {
		switch name {
		case columnGroup:
			row[index] = "1"
		case columnPrice, columnHeight, columnLength, columnWidth, columnWeightBrutto:
			row[index] = "1"
		default:
			row[index] = "значение"
		}
	}
	headers := append([]string(nil), requiredColumns...)
	for index, name := range headers {
		switch name {
		case columnVendorCode:
			headers[index] = "vendorcode"
		case columnWeightBrutto:
			headers[index] = "weightbrutto"
		}
	}
	columns, err := newColumns(headers)
	if err != nil {
		t.Fatal(err)
	}
	parsed, issues := parseRow("Товары", 3, row, columns)
	if parsed.Group != "1" || hasErrors(issues) {
		t.Fatalf("заполненная группа отклонена: group=%q issues=%+v", parsed.Group, issues)
	}

	row[columns.indexesByName[columnGroup][0]] = "   "
	_, issues = parseRow("Товары", 3, row, columns)
	if len(issues) != 1 || issues[0].Code != "group_required" ||
		issues[0].Severity != cardimport_model.IssueSeverityError {
		t.Fatalf("пустая группа не отклонена: %+v", issues)
	}
}
