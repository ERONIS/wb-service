package statistics_telegram_transport

import (
	"context"
	"testing"

	statistics_service "github.com/ERONIS/wb-service/internal/feature/statistics/service"
	tele "gopkg.in/telebot.v3"
)

type attentionListStatistics struct {
	StatisticsService
	filter statistics_service.AttentionFilter
	called bool
}

type attentionListContext struct {
	tele.Context
	args     []string
	edited   bool
	notified bool
}

func (s *attentionListStatistics) ListAttention(
	_ context.Context,
	filter statistics_service.AttentionFilter,
) ([]statistics_service.AttentionRow, error) {
	s.filter = filter
	s.called = true
	return nil, nil
}

func (c *attentionListContext) Args() []string {
	return c.args
}

func (c *attentionListContext) EditOrSend(_ interface{}, _ ...interface{}) error {
	c.edited = true
	return nil
}

func (c *attentionListContext) NotifyMenu(_, _ string) error {
	c.notified = true
	return nil
}

func TestListAttentionCallbackArguments(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantTransferID int64
	}{
		{name: "empty callback payload", args: []string{""}},
		{name: "transfer filter", args: []string{"z"}, wantTransferID: 35},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statistics := &attentionListStatistics{}
			ctx := &attentionListContext{args: tt.args}
			handler := &Handler{
				ctx:        context.Background(),
				bot:        &tele.Bot{},
				statistics: statistics,
			}
			if err := handler.listAttention(ctx); err != nil {
				t.Fatalf("listAttention() error = %v", err)
			}
			if !statistics.called {
				t.Fatal("ListAttention was not called")
			}
			if ctx.notified {
				t.Fatal("callback was treated as stale")
			}
			if statistics.filter.TransferID != tt.wantTransferID {
				t.Fatalf("TransferID = %d, want %d", statistics.filter.TransferID, tt.wantTransferID)
			}
			if !ctx.edited {
				t.Fatal("attention list was not rendered")
			}
		})
	}
}
