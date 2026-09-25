package core_transport_telegram

import (
	"html"
	"strings"
	"testing"
)

func TestLimitMenuTextPreservesShortHTML(t *testing.T) {
	t.Parallel()

	text := "📦 <b>Загрузка</b> &amp; проверка"
	if actual := limitMenuText(text); actual != text {
		t.Fatalf("limitMenuText() = %q, want unchanged %q", actual, text)
	}
}

func TestLimitMenuTextSafelyTruncatesHTML(t *testing.T) {
	t.Parallel()

	text := "<b>Заголовок</b>\n" + strings.Repeat("данные &amp; ещё данные ", 400)
	actual := limitMenuText(text)
	plainText := plainTextFromMenuHTML(actual)

	if telegramTextUnits(plainText) > menuTextUnitLimit {
		t.Fatalf(
			"visible menu length = %d units, want at most %d",
			telegramTextUnits(plainText),
			menuTextUnitLimit,
		)
	}
	if !strings.HasSuffix(actual, truncatedMenuSuffix) {
		t.Fatalf("limitMenuText() does not contain truncation suffix: %q", actual)
	}
	if strings.Contains(actual, "<b>") {
		t.Fatalf("limitMenuText() retained source formatting in truncated text: %q", actual)
	}
	if !strings.Contains(actual, html.EscapeString("данные & ещё данные")) {
		t.Fatalf("limitMenuText() did not safely escape visible text: %q", actual)
	}
}

func TestLimitMenuTextCountsEmojiAsTwoUTF16Units(t *testing.T) {
	t.Parallel()

	actual := limitMenuText(strings.Repeat("📦", menuTextUnitLimit))
	if units := telegramTextUnits(plainTextFromMenuHTML(actual)); units > menuTextUnitLimit {
		t.Fatalf("visible menu length = %d units, want at most %d", units, menuTextUnitLimit)
	}
}

func TestPlainTextFromMenuHTMLPreservesEscapedAngles(t *testing.T) {
	t.Parallel()

	actual := plainTextFromMenuHTML("<b>Файл</b>: a&lt;b &amp; c&gt;d")
	want := "Файл: a<b & c>d"
	if actual != want {
		t.Fatalf("plainTextFromMenuHTML() = %q, want %q", actual, want)
	}
}
