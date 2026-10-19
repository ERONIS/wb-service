package core_tg_identity

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	core_tg_ui "github.com/ERONIS/wb-service/internal/core/transport/telegram/ui"

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

	return domain.CreateUser(telegramID, core_tg_ui.TruncateRunes(name, 100)), nil
}
