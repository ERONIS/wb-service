package cabinetcopy_wb_transport

import (
	"context"
	"testing"

	wbcabinet_service "github.com/ERONIS/wb-service/internal/feature/wbcabinet/service"
)

func TestCabinetsForOwnerUsesDurableOwnershipSource(t *testing.T) {
	reader := NewReader(
		readerClientsetStub{},
		ownerCabinetSourceStub{byOwner: map[int64][]wbcabinet_service.TargetCredential{
			101: {{CabinetID: "owned", Name: "Owned cabinet"}},
			202: {{CabinetID: "foreign", Name: "Foreign cabinet"}},
		}},
	)

	cabinets, err := reader.CabinetsForOwner(context.Background(), 101)
	if err != nil {
		t.Fatalf("CabinetsForOwner() error = %v", err)
	}
	if len(cabinets) != 1 || cabinets[0].ID != "owned" || cabinets[0].Name != "Owned cabinet" {
		t.Fatalf("CabinetsForOwner() = %+v", cabinets)
	}
}

type ownerCabinetSourceStub struct {
	byOwner map[int64][]wbcabinet_service.TargetCredential
}

func (source ownerCabinetSourceStub) Targets(
	_ context.Context,
	ownerTelegramID int64,
) ([]wbcabinet_service.TargetCredential, error) {
	return append([]wbcabinet_service.TargetCredential(nil), source.byOwner[ownerTelegramID]...), nil
}

var _ CabinetSource = ownerCabinetSourceStub{}
