package statistics_telegram_transport

import (
	"testing"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
)

func TestCabinetTaskStatusUsesItemActions(t *testing.T) {
	cases := []struct {
		name string
		task statistics_service.CabinetTaskRow
		want string
	}{
		{
			name: "product reconciliation precedes planned media",
			task: statistics_service.CabinetTaskRow{
				State: "running", OverallOutcome: "running", PublicationStatus: "running",
				ProductActionState: "reconciling", MediaStatus: "not_started", MediaActionState: "planned",
			},
			want: "🕐 Проверяется в WB",
		},
		{
			name: "product dispatch",
			task: statistics_service.CabinetTaskRow{
				State: "running", OverallOutcome: "running", PublicationStatus: "running",
				ProductActionState: "dispatching", MediaStatus: "not_started", MediaActionState: "planned",
			},
			want: "🛠️ Создаётся карточка",
		},
		{
			name: "planned product",
			task: statistics_service.CabinetTaskRow{
				State: "running", OverallOutcome: "running", PublicationStatus: "running",
				ProductActionState: "planned",
			},
			want: "🟡 Отправка в очереди",
		},
		{
			name: "terminal product with planned media",
			task: statistics_service.CabinetTaskRow{
				State: "terminal", OutcomeClass: "success", OverallOutcome: "running",
				PublicationStatus: "succeeded", MediaStatus: "not_started", MediaActionState: "planned",
			},
			want: "🟡 Фото в очереди",
		},
		{
			name: "terminal product with media reconciliation",
			task: statistics_service.CabinetTaskRow{
				State: "terminal", OutcomeClass: "success", OverallOutcome: "running",
				PublicationStatus: "succeeded", MediaStatus: "running", MediaActionState: "reconciling",
			},
			want: "🕐 Проверяются фото в WB",
		},
		{
			name: "running preparation while another group may publish",
			task: statistics_service.CabinetTaskRow{
				State: "running", OverallOutcome: "running", PreparationStatus: "running",
				PublicationStatus: "not_started", MediaStatus: "not_started",
			},
			want: "🔎 Проверяется",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cabinetTaskStatus(tc.task); got != tc.want {
				t.Fatalf("cabinetTaskStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}
