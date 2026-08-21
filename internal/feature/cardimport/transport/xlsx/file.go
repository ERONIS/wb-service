package cardimport_xlsx_transport

import "strings"

const xlsxMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// IsFile сообщает, похожи ли имя или MIME type на XLSX.
// Окончательная проверка формата выполняется при открытии workbook.
func IsFile(filename string, mimeType string) bool {
	filename = strings.ToLower(strings.TrimSpace(filename))
	if strings.HasSuffix(filename, ".xlsx") {
		return true
	}

	return strings.EqualFold(strings.TrimSpace(mimeType), xlsxMIME)
}
