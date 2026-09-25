package core_transport_telegram

import (
	"fmt"
	"strconv"
	"strings"

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

func SenderID(ctx tele.Context) (int64, error) {
	if ctx == nil || ctx.Sender() == nil || ctx.Sender().ID <= 0 {
		return 0, core_errors.InvalidArgument("Telegram sender is unavailable")
	}
	return ctx.Sender().ID, nil
}

func ActorFromContext(ctx tele.Context) (domain.Actor, error) {
	telegramID, err := SenderID(ctx)
	if err != nil {
		return domain.Actor{}, err
	}
	sender := ctx.Sender()
	displayName := strings.TrimSpace(strings.Join(
		[]string{sender.FirstName, sender.LastName},
		" ",
	))
	if displayName == "" {
		displayName = strings.TrimSpace(sender.Username)
	}
	if displayName == "" {
		displayName = fmt.Sprintf("Telegram %d", telegramID)
	}
	displayName = TruncateRunes(displayName, domain.MaxActorDisplayNameLength)

	actor := domain.Actor{
		TelegramUserID: telegramID,
		DisplayName:    displayName,
	}.Normalized()
	if err := actor.Validate(); err != nil {
		return domain.Actor{}, err
	}
	return actor, nil
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
