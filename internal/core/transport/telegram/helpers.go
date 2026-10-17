package core_transport_telegram

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"

	tele "gopkg.in/telebot.v3"
)

// ParseTelegramID разбирает положительный Telegram ID.
func ParseTelegramID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	telegramID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || telegramID <= 0 {
		return 0, fmt.Errorf("invalid Telegram ID %q", value)
	}

	return telegramID, nil
}

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

func SenderID(ctx tele.Context) (int64, error) {
	if ctx == nil || ctx.Sender() == nil || ctx.Sender().ID <= 0 {
		return 0, core_errors.InvalidArgument("Telegram sender is unavailable")
	}
	return ctx.Sender().ID, nil
}

// UserFromContext извлекает профиль пользователя из контекста Telegram.
func UserFromContext(ctx tele.Context) (domain.User, error) {
	telegramID, err := SenderID(ctx)
	if err != nil {
		return domain.User{}, err
	}
	sender := ctx.Sender()
	name := strings.TrimSpace(sender.FirstName + " " + sender.LastName)
	if name == "" {
		name = strings.TrimSpace(sender.Username)
	}
	if utf8.RuneCountInString(name) < 3 {
		name = fmt.Sprintf("Telegram %d", telegramID)
	}

	return domain.CreateUser(telegramID, TruncateRunes(name, 100)), nil
}

func TruncateRunes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	return string(runes[:maximum-1]) + "…"
}
