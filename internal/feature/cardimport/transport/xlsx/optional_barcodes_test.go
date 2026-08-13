package cardimport_xlsx_transport

import "testing"

func TestColumnsAllowMissingBarcodeColumn(t *testing.T) {
	t.Parallel()

	headers := requiredHeadersWithoutBarcodes()
	if _, err := newColumns(headers); err != nil {
		t.Fatalf("newColumns() error = %v", err)
	}
}

func TestParseRowAllowsMissingBarcodes(t *testing.T) {
	t.Parallel()

	headers := requiredHeadersWithoutBarcodes()
	parsedColumns, err := newColumns(headers)
	if err != nil {
		t.Fatalf("newColumns() error = %v", err)
	}
	row := []string{
		"group-1",
		"vendor-1",
		"Title",
		"Category",
		"Brand",
		"Description",
		"1.2",
		"100",
		"10",
		"20",
		"30",
	}

	parsed, issues := parseRow("Товары", 3, row, parsedColumns)
	if len(issues) != 0 {
		t.Fatalf("parseRow() issues = %#v, want none", issues)
	}
	if len(parsed.Variant.Sizes) != 1 {
		t.Fatalf("sizes count = %d, want 1", len(parsed.Variant.Sizes))
	}
	if len(parsed.Variant.Sizes[0].SKUs) != 0 {
		t.Fatalf("SKUs = %#v, want empty", parsed.Variant.Sizes[0].SKUs)
	}
}

func TestParseRowAllowsEmptyGroupForSingletonCard(t *testing.T) {
	t.Parallel()

	headers := requiredHeadersWithoutBarcodes()
	parsedColumns, err := newColumns(headers)
	if err != nil {
		t.Fatalf("newColumns() error = %v", err)
	}
	row := []string{
		"",
		"vendor-1",
		"Title",
		"Category",
		"Brand",
		"Description",
		"1.2",
		"100",
		"10",
		"20",
		"30",
	}

	parsed, issues := parseRow("Товары", 3, row, parsedColumns)
	if len(issues) != 0 {
		t.Fatalf("parseRow() issues = %#v, want none", issues)
	}
	if parsed.Group != "" {
		t.Fatalf("group = %q, want empty singleton group", parsed.Group)
	}
}

func requiredHeadersWithoutBarcodes() []string {
	return []string{
		"group",
		"vendorcode",
		"title",
		"category",
		"brand",
		"description",
		"weightbrutto",
		"price",
		"height",
		"length",
		"width",
	}
}
