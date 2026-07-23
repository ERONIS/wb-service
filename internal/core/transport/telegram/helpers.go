package core_transport_telegram

import (
	"fmt"
	"strconv"
	"strings"

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

// MainMenuButton возвращает кнопку перехода в главное меню.
func MainMenuButton() tele.Btn {
	return tele.Btn{
		Text:   "🏠 Главное меню",
		Unique: CallbackMainMenu,
	}
}
