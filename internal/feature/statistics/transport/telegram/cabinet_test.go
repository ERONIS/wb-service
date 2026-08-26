package statistics_telegram_transport

import (
	"strings"
	"testing"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
)

func TestSummarizeCabinetProgress(t *testing.T) {
	t.Parallel()

	summary := summarizeCabinetProgress([]statistics_service.CabinetProgressRow{
		{Total: 5, Pending: 1, Running: 1, Terminal: 3, Ready: 2, Errors: 1},
		{Total: 5, Pending: 2, Running: 0, Terminal: 3, Ready: 3},
	})
	want := (taskSummary{Total: 10, Pending: 3, Running: 1, Terminal: 6, Ready: 5, Errors: 1})
	if summary != want {
		t.Fatalf("summarizeCabinetProgress() = %#v, want %#v", summary, want)
	}
	text := formatTaskSummary(summary)
	for _, fragment := range []string{
		"[######....] 60%",
		"🟡 В очереди: 3",
		"🔵 В работе: 1",
		"🔴 Ошибки: 1",
		"🟢 Готово: 5",
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("formatTaskSummary() misses %q in %q", fragment, text)
		}
	}
}

func TestCabinetButtonTextUsesConfiguredNameAndProgress(t *testing.T) {
	t.Parallel()

	handler := &Handler{cabinetNames: map[string]string{"main": "Основной кабинет"}}
	got := handler.cabinetButtonText(statistics_service.CabinetProgressRow{
		CabinetID: "main",
		Total:     8,
		Running:   2,
		Terminal:  3,
	})
	if got != "🔵 Основной кабинет · 38%" {
		t.Fatalf("cabinetButtonText() = %q", got)
	}
}

func TestCabinetTaskStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		task  statistics_service.CabinetTaskRow
		phase string
		want  string
	}{
		{name: "pending", task: statistics_service.CabinetTaskRow{State: "pending"}, want: "🟡 В очереди"},
		{name: "publishing", task: statistics_service.CabinetTaskRow{State: "running"}, phase: "publishing", want: "🛠️ Создаётся карточка"},
		{name: "media", task: statistics_service.CabinetTaskRow{State: "running"}, phase: "media", want: "📸 Загружается фото"},
		{name: "media after card creation", task: statistics_service.CabinetTaskRow{State: "terminal", OutcomeClass: "success", OverallOutcome: "running", MediaStatus: "running"}, phase: "media", want: "📸 Загружается фото"},
		{name: "success", task: statistics_service.CabinetTaskRow{State: "terminal", OutcomeClass: "success", OverallOutcome: "success", NMID: 123}, want: "✅ Готово · nmID 123"},
		{name: "attention", task: statistics_service.CabinetTaskRow{State: "terminal", OutcomeClass: "unresolved"}, want: "⚠️ Нужна проверка"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := cabinetTaskStatus(test.task, test.phase); got != test.want {
				t.Fatalf("cabinetTaskStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCabinetTaskPages(t *testing.T) {
	t.Parallel()

	for total, want := range map[int64]int64{0: 1, 1: 1, 10: 1, 11: 2, 21: 3} {
		if got := cabinetTaskPages(total); got != want {
			t.Errorf("cabinetTaskPages(%d) = %d, want %d", total, got, want)
		}
	}
}

func TestEscapeLimitedDoesNotSplitHTMLEntity(t *testing.T) {
	t.Parallel()

	if got := escapeLimited("&&", 6); got != "&amp;…" {
		t.Fatalf("escapeLimited() = %q", got)
	}
}
