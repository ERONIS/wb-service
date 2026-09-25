package cardedit_service

import (
	"context"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"

	"go.uber.org/zap"
)

func TestFindUsesExactVendorCodeAndSortsTargets(t *testing.T) {
	t.Parallel()

	var generation domain.ClientGeneration
	generation[0] = 1
	gateway := &gatewayStub{
		cabinets: []Cabinet{
			{ID: "cabinet-b", Name: "B", BindingRevision: 1, CapabilityRevision: 1, ClientGeneration: generation},
			{ID: "cabinet-a", Name: "A", BindingRevision: 1, CapabilityRevision: 1, ClientGeneration: generation},
		},
		cards: map[domain.CabinetID][]contentapi.Card{
			"cabinet-b": {
				{NMID: 3, VendorCode: "sku-extra"},
				{NMID: 2, VendorCode: " SKU "},
			},
			"cabinet-a": {{NMID: 1, VendorCode: "sku"}},
		},
	}
	service := New(context.Background(), gateway, Config{
		PollInterval: time.Millisecond,
		MaxAttempts:  1,
		Workers:      2,
		QueueSize:    2,
	}, zap.NewNop())

	targets, err := service.Find(context.Background(), 100, "sku")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 exact targets, got %#v", targets)
	}
	if targets[0].Cabinet.Name != "A" || targets[0].NMID != 1 || targets[1].Cabinet.Name != "B" || targets[1].NMID != 2 {
		t.Fatalf("targets are not sorted or exact: %#v", targets)
	}
}
