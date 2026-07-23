package cards_xlsx_transport

import (
	"regexp"
	"strconv"
	"strings"
)

var numberPattern = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)

func parseBool(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "t", "да", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseInt(value string) int {
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return 0
	}

	number, _ := strconv.Atoi(value)
	return number
}

func parseFloat(value string) float64 {
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return 0
	}

	if match := numberPattern.FindString(value); match != "" {
		value = match
	}

	value = strings.ReplaceAll(value, ",", ".")
	number, _ := strconv.ParseFloat(value, 64)
	return number
}

func parseGroupID(value string) int {
	if number := parseInt(value); number != 0 {
		return number
	}
	if number := parseFloat(value); number > 0 {
		return int(number)
	}

	return 0
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' ||
			character == ';' ||
			character == '\n' ||
			character == '\r'
	})

	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
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
