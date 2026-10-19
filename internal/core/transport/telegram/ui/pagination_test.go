package core_tg_ui_test

import (
	"testing"

	core_tg_ui "github.com/ERONIS/wb-service/internal/core/transport/telegram/ui"
	tele "gopkg.in/telebot.v3"
)

func TestCalculateTotalPages(t *testing.T) {
	tests := []struct {
		total    int64
		pageSize int64
		want     int64
	}{
		{0, 10, 1},
		{-5, 10, 1},
		{5, 0, 1},
		{1, 10, 1},
		{10, 10, 1},
		{11, 10, 2},
		{25, 10, 3},
		{100, 10, 10},
	}

	for _, tt := range tests {
		got := core_tg_ui.CalculateTotalPages(tt.total, tt.pageSize)
		if got != tt.want {
			t.Errorf("CalculateTotalPages(%d, %d) = %d, want %d", tt.total, tt.pageSize, got, tt.want)
		}
	}
}

func TestBuildPaginationRow(t *testing.T) {
	markup := &tele.ReplyMarkup{}

	// Случай: всего 1 страница — кнопок быть не должно
	_, ok := core_tg_ui.BuildPaginationRow(markup, core_tg_ui.PaginationConfig{
		Page:           1,
		TotalPages:     1,
		CallbackUnique: "test_page",
	})
	if ok {
		t.Errorf("expected no pagination row for single page")
	}

	// Случай: первая страница из трёх — только кнопка «Вперёд»
	row, ok := core_tg_ui.BuildPaginationRow(markup, core_tg_ui.PaginationConfig{
		Page:           1,
		TotalPages:     3,
		CallbackUnique: "test_page",
	})
	if !ok {
		t.Fatalf("expected pagination row")
	}
	if len(row) != 1 {
		t.Fatalf("expected 1 button on first page, got %d", len(row))
	}
	if row[0].Text != "Вперёд ➡️" {
		t.Errorf("expected Next button, got %s", row[0].Text)
	}

	// Случай: вторая страница из трёх — кнопки «Назад» и «Вперёд»
	row, ok = core_tg_ui.BuildPaginationRow(markup, core_tg_ui.PaginationConfig{
		Page:           2,
		TotalPages:     3,
		CallbackUnique: "test_page",
		ArgPrefix:      []string{"prefix"},
	})
	if !ok || len(row) != 2 {
		t.Fatalf("expected 2 buttons on middle page, got %d", len(row))
	}
	if row[0].Text != "⬅️ Назад" || row[1].Text != "Вперёд ➡️" {
		t.Errorf("unexpected button texts: %s, %s", row[0].Text, row[1].Text)
	}

	// Случай: третья страница из трёх — только кнопка «Назад»
	row, ok = core_tg_ui.BuildPaginationRow(markup, core_tg_ui.PaginationConfig{
		Page:           3,
		TotalPages:     3,
		CallbackUnique: "test_page",
	})
	if !ok || len(row) != 1 {
		t.Fatalf("expected 1 button on last page, got %d", len(row))
	}
	if row[0].Text != "⬅️ Назад" {
		t.Errorf("expected Prev button, got %s", row[0].Text)
	}
}
