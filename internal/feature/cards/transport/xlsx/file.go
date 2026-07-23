package cards_xlsx_transport

import "strings"

const (
	xlsxMIME  = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	excelMIME = "application/vnd.ms-excel"
)

// IsFile reports whether a filename or MIME type identifies an XLSX document.
func IsFile(filename string, mimeType string) bool {
	filename = strings.ToLower(strings.TrimSpace(filename))
	if strings.HasSuffix(filename, ".xlsx") {
		return true
	}

	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case xlsxMIME, excelMIME:
		return true
	default:
		return false
	}
}
