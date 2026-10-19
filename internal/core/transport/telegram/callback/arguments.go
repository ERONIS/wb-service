package core_tg_callback

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseInt64Argument(
	arguments []string,
	index int,
	expected int,
	base int,
	minimum int64,
) (int64, error) {
	if expected < 0 || len(arguments) != expected || index < 0 ||
		index >= len(arguments) {
		return 0, fmt.Errorf("invalid callback arguments")
	}
	value, err := strconv.ParseInt(arguments[index], base, 64)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("invalid callback integer %q", arguments[index])
	}
	return value, nil
}

// EncodeID кодирует целое число в компактную Base-36 строку для экономии байт в callback_data (лимит 64 байта).
func EncodeID(id int64) string {
	return strconv.FormatInt(id, 36)
}

// ParseID декодирует целое число из Base-36 строки.
func ParseID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty Base-36 ID")
	}
	val, err := strconv.ParseInt(value, 36, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Base-36 ID %q: %w", value, err)
	}
	return val, nil
}

// ParseBase36Argument парсит Base-36 аргумент из списка callback-аргументов.
func ParseBase36Argument(
	arguments []string,
	index int,
	expected int,
	minimum int64,
) (int64, error) {
	return ParseInt64Argument(arguments, index, expected, 36, minimum)
}

// ParseIDAndRevision извлекает ID сущности и номер ревизии из первых двух аргументов callback.
func ParseIDAndRevision(arguments []string) (int64, int64, error) {
	if len(arguments) < 2 {
		return 0, 0, fmt.Errorf("insufficient callback arguments: expected at least 2, got %d", len(arguments))
	}
	id, err := ParseInt64Argument(arguments, 0, len(arguments), 10, 1)
	if err != nil {
		return 0, 0, err
	}
	revision, err := ParseInt64Argument(arguments, 1, len(arguments), 10, 0)
	if err != nil {
		return 0, 0, err
	}
	return id, revision, nil
}

// ParsePageNumber разбирает номер страницы (по умолчанию 1).
func ParsePageNumber(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return 0, fmt.Errorf("invalid page %q", value)
	}
	return page, nil
}
