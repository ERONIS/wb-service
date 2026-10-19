package core_tg_menu

import (
	"html"
	"strings"
	"unicode/utf16"
)

const menuTextUnitLimit = 4096

const truncatedMenuSuffix = "\n\n<i>Часть данных скрыта из-за лимита Telegram.</i>"

func limitMenuText(text string) string {
	plainText := plainTextFromMenuHTML(text)
	if telegramTextUnits(plainText) <= menuTextUnitLimit {
		return text
	}

	suffixUnits := telegramTextUnits(plainTextFromMenuHTML(truncatedMenuSuffix))
	plainText = truncateTelegramText(
		strings.TrimSpace(plainText),
		menuTextUnitLimit-suffixUnits,
	)
	return html.EscapeString(plainText) + truncatedMenuSuffix
}

func truncateTelegramText(text string, maximumUnits int) string {
	if maximumUnits <= 0 {
		return ""
	}
	if telegramTextUnits(text) <= maximumUnits {
		return text
	}

	const ellipsis = '…'
	remaining := maximumUnits - utf16.RuneLen(ellipsis)
	var builder strings.Builder
	for _, current := range text {
		units := utf16.RuneLen(current)
		if units < 1 {
			units = 1
		}
		if units > remaining {
			break
		}
		builder.WriteRune(current)
		remaining -= units
	}
	builder.WriteRune(ellipsis)
	return builder.String()
}

func telegramTextUnits(text string) int {
	units := 0
	for _, current := range text {
		count := utf16.RuneLen(current)
		if count < 1 {
			count = 1
		}
		units += count
	}
	return units
}

func plainTextFromMenuHTML(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	inTag := false
	for _, current := range text {
		switch current {
		case '<':
			inTag = true
		case '>':
			if inTag {
				inTag = false
				continue
			}
			builder.WriteRune(current)
		default:
			if !inTag {
				builder.WriteRune(current)
			}
		}
	}
	return html.UnescapeString(builder.String())
}
