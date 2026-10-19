package core_tg_ui_test

import (
	"testing"
	"time"

	core_tg_ui "github.com/ERONIS/wb-service/internal/core/transport/telegram/ui"
)

func TestFormatProgressBar(t *testing.T) {
	tests := []struct {
		done  int64
		total int64
		width int
		want  string
	}{
		{0, 10, 10, "[..........]"},
		{5, 10, 10, "[#####.....]"},
		{10, 10, 10, "[##########]"},
		{15, 10, 10, "[##########]"},
		{-1, 10, 10, "[..........]"},
		{0, 0, 10, ""},
		{5, 10, 0, ""},
	}

	for _, tt := range tests {
		got := core_tg_ui.FormatProgressBar(tt.done, tt.total, tt.width)
		if got != tt.want {
			t.Errorf("FormatProgressBar(%d, %d, %d) = %q, want %q", tt.done, tt.total, tt.width, got, tt.want)
		}
	}
}

func TestCalcPercent(t *testing.T) {
	tests := []struct {
		done  int64
		total int64
		want  int64
	}{
		{0, 100, 0},
		{50, 100, 50},
		{1, 3, 33},
		{2, 3, 67},
		{100, 100, 100},
		{120, 100, 100},
		{-5, 100, 0},
		{10, 0, 0},
	}

	for _, tt := range tests {
		got := core_tg_ui.CalcPercent(tt.done, tt.total)
		if got != tt.want {
			t.Errorf("CalcPercent(%d, %d) = %d, want %d", tt.done, tt.total, got, tt.want)
		}
	}
}

func TestFormatDateTime(t *testing.T) {
	date := time.Date(2026, 9, 27, 12, 30, 0, 0, time.UTC)
	formattedUTC := core_tg_ui.FormatDateTimeUTC(date)
	if formattedUTC != "27.09.2026 12:30 UTC" {
		t.Errorf("unexpected UTC format: %s", formattedUTC)
	}

	zeroFormatted := core_tg_ui.FormatDateTime(time.Time{})
	if zeroFormatted != "—" {
		t.Errorf("expected '—', got %q", zeroFormatted)
	}
}
