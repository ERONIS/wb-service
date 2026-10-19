package core_tg_menu

import (
	"strings"
	"testing"
)

func TestLimitMenuTextPreservesShortMarkup(t *testing.T) {
	const text = "<b>Название</b> &amp; описание"
	if got := limitMenuText(text); got != text {
		t.Fatalf("short menu changed: %q", got)
	}
}

func TestLimitMenuTextTruncatesUTF16AndEscapesMarkup(t *testing.T) {
	text := "<b>" + strings.Repeat("😀", menuTextUnitLimit/2) + "</b><i>&amp;</i>"
	got := limitMenuText(text)
	if got == text {
		t.Fatal("oversized menu was not truncated")
	}
	if units := telegramTextUnits(plainTextFromMenuHTML(got)); units > menuTextUnitLimit {
		t.Fatalf("truncated menu has %d UTF-16 units", units)
	}
	if !strings.HasSuffix(got, truncatedMenuSuffix) {
		t.Fatalf("truncation marker missing: %q", got[len(got)-100:])
	}
	if strings.Contains(got, "<b>") {
		t.Fatal("truncated menu retained an unclosed formatting tag")
	}
}
