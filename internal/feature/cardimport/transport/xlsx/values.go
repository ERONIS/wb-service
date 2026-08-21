package cardimport_xlsx_transport

import (
	"fmt"
	"strconv"
	"strings"
)

func parsePositiveInt64(value string) (int64, error) {
	normalized := normalizeNumber(value)
	if normalized == "" {
		return 0, fmt.Errorf("value is empty")
	}

	number, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("expected a positive integer, got %q", value)
	}

	return number, nil
}

func parsePositiveFloat64(value string) (float64, error) {
	normalized := strings.ReplaceAll(normalizeNumber(value), ",", ".")
	if normalized == "" {
		return 0, fmt.Errorf("value is empty")
	}

	number, err := strconv.ParseFloat(normalized, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("expected a positive number, got %q", value)
	}

	return number, nil
}

func normalizeNumber(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "\u00a0", "")

	return value
}

func splitValues(value string, comma bool) []string {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == ';' ||
			character == '\n' ||
			character == '\r' ||
			(comma && character == ',')
	})

	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}

		seen[part] = struct{}{}
		values = append(values, part)
	}

	return values
}

func isRowEmpty(row []string) bool {
	for _, value := range row {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}

	return true
}
